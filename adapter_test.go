package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/NARUBROWN/spine/contract/http"
)

func newConfiguredAdapter(t *testing.T, config adapter.Config, options ...NetHTTPOption) *NetHTTPAdapter {
	t.Helper()
	value, err := NewFactory(options...).New(config)
	if err != nil {
		t.Fatal(err)
	}
	return value.(*NetHTTPAdapter)
}

func newTestAdapter(t *testing.T, options adapter.ServerOptions) *NetHTTPAdapter {
	t.Helper()
	return newConfiguredAdapter(t, adapter.Config{Options: options, Execute: func(adapter.HTTPContext) error { return nil }})
}

func TestFactoryValidatesBeforeNativeMiddleware(t *testing.T) {
	var configured atomic.Int32
	f := NewFactory(WithMiddleware(func(next http.Handler) http.Handler { configured.Add(1); return next }))
	for _, config := range []adapter.Config{
		{},
		{Execute: func(adapter.HTTPContext) error { return nil }, Options: adapter.ServerOptions{ReadTimeout: -1}},
		{Execute: func(adapter.HTTPContext) error { return nil }, Options: adapter.ServerOptions{DisableRecover: true}},
	} {
		if _, err := f.New(config); err == nil {
			t.Fatal("잘못된 설정은 초기화 전에 거부해야 합니다")
		}
	}
	if configured.Load() != 0 {
		t.Fatal("잘못된 설정에서 native 초기화가 실행되었습니다")
	}
	cfg := adapter.Config{Execute: func(adapter.HTTPContext) error { return nil }}
	if _, err := f.(adapter.NetHTTPUpgradeFactory).NewWithNetHTTPUpgrades(cfg, []adapter.NetHTTPUpgradeRoute{{Path: "/ws"}}); !errors.Is(err, adapter.ErrInvalidAdapter) || configured.Load() != 0 {
		t.Fatalf("업그레이드 검사 전에 초기화하면 안 됩니다: %v", err)
	}
	for _, middleware := range []func(http.Handler) http.Handler{nil, func(http.Handler) http.Handler { return (*http.ServeMux)(nil) }} {
		if _, err := NewFactory(WithMiddleware(middleware)).New(cfg); !errors.Is(err, adapter.ErrInvalidAdapter) {
			t.Fatalf("nil middleware와 결과를 거부해야 합니다: %v", err)
		}
	}
}

func TestDefaultsAndExplicitOptions(t *testing.T) {
	defaults := newTestAdapter(t, adapter.ServerOptions{})
	if defaults.httpServer.ReadHeaderTimeout != 5*time.Second || defaults.httpServer.ReadTimeout != 30*time.Second || defaults.httpServer.WriteTimeout != 30*time.Second || defaults.httpServer.IdleTimeout != 120*time.Second || defaults.httpServer.MaxHeaderBytes != 1<<20 || defaults.maxBodyBytes != 32<<20 {
		t.Fatal("플랫폼 기본값이 일치하지 않습니다")
	}
	opts := adapter.ServerOptions{ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second, MaxHeaderBytes: 4096, MaxBodyBytes: -1}
	s := newTestAdapter(t, opts)
	if s.httpServer.ReadHeaderTimeout != opts.ReadHeaderTimeout || s.httpServer.ReadTimeout != opts.ReadTimeout || s.httpServer.WriteTimeout != opts.WriteTimeout || s.httpServer.IdleTimeout != opts.IdleTimeout || s.httpServer.MaxHeaderBytes != opts.MaxHeaderBytes || s.maxBodyBytes != -1 {
		t.Fatal("명시한 옵션을 그대로 적용해야 합니다")
	}
}

type readCounter struct {
	calls  atomic.Int32
	source io.Reader
}

func (r *readCounter) Read(p []byte) (int, error) { r.calls.Add(1); return r.source.Read(p) }
func (*readCounter) Close() error                 { return nil }

func TestRawSnapshotsGetterAndRequestCancellation(t *testing.T) {
	reader := &readCounter{source: strings.NewReader("abc")}
	request := httptest.NewRequest("POST", "/a%252Fb/../c//d?tags=a%2Cb,c&tags=d", nil)
	request.Body = reader
	request.Host = "[::1]:8443"
	request.Header.Set("X-ID", "original")
	request.Header["host"] = []string{"ignored"}
	var retained adapter.HTTPContext
	s := newConfiguredAdapter(t, adapter.Config{Execute: func(scope adapter.HTTPContext) error {
		retained = scope
		if scope.RawPath() != "/a%252Fb/../c//d" || scope.RawQuery() != "tags=a%2Cb,c&tags=d" || scope.Header("HOST") != request.Host {
			t.Error("원시 경로, 쿼리와 Host를 보존해야 합니다")
		}
		one, two := scope.Headers(), scope.Headers()
		one["X-Id"][0] = "changed"
		if two["X-Id"][0] != "original" || request.Header.Get("X-ID") != "original" {
			t.Error("헤더 맵과 슬라이스는 독립 스냅샷이어야 합니다")
		}
		_ = scope.Body()
		_ = scope.Body()
		if reader.calls.Load() != 0 {
			t.Error("getter에서 본문을 읽으면 안 됩니다")
		}
		buf := make([]byte, 1)
		_, _ = scope.Body().Read(buf)
		data, err := io.ReadAll(scope.Body())
		if err != nil || string(buf)+string(data) != "abc" {
			t.Errorf("같은 읽기 위치를 유지해야 합니다: %q %v", data, err)
		}
		return scope.Writer().WriteStatus(204)
	}})
	s.Handler().ServeHTTP(httptest.NewRecorder(), request)
	request.URL.RawQuery = "changed"
	request.Header.Set("X-ID", "changed")
	if retained.RawQuery() != "tags=a%2Cb,c&tags=d" || retained.Header("X-ID") != "original" || retained.Context().Err() != context.Canceled {
		t.Fatal("반환 후 스냅샷과 요청 종료 취소를 보존해야 합니다")
	}
}

func TestBodyLimitPreservesNativeCause(t *testing.T) {
	for _, contentLength := range []int64{3, -1} {
		t.Run(map[int64]string{3: "fixed", -1: "unknown"}[contentLength], func(t *testing.T) {
			s := newConfiguredAdapter(t, adapter.Config{Options: adapter.ServerOptions{MaxBodyBytes: 2}, Execute: func(scope adapter.HTTPContext) error {
				_, err := io.ReadAll(scope.Body())
				var requestError *adapter.RequestError
				var limit *http.MaxBytesError
				if !errors.As(err, &requestError) || requestError.Status != 413 || !errors.As(err, &limit) {
					t.Errorf("413과 native 원인을 보존해야 합니다: %v", err)
				}
				return scope.Writer().WriteStatus(413)
			}})
			r := httptest.NewRequest("POST", "/", strings.NewReader("abc"))
			r.ContentLength = contentLength
			s.Handler().ServeHTTP(httptest.NewRecorder(), r)
		})
	}
}

func TestMiddlewareOrderNativeUpgradeAndStop(t *testing.T) {
	var order []string
	wrap := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+"-in")
				next.ServeHTTP(w, r)
				order = append(order, name+"-out")
			})
		}
	}
	f := NewFactory(WithMiddleware(wrap("first"), wrap("second")))
	s, err := f.(adapter.NetHTTPUpgradeFactory).NewWithNetHTTPUpgrades(adapter.Config{Execute: func(adapter.HTTPContext) error {
		t.Error("native 업그레이드는 Execute를 거치면 안 됩니다")
		return nil
	}}, []adapter.NetHTTPUpgradeRoute{{Path: "/ws", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "upgrade")
		w.WriteHeader(202)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := adapter.NetHTTPHandler(s)
	if err != nil {
		t.Fatal(err)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil))
	if !reflect.DeepEqual(order, []string{"first-in", "second-in", "upgrade", "second-out", "first-out"}) {
		t.Fatalf("미들웨어 순서: %v", order)
	}
	s.StopAccepting()
	s.StopAccepting()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ws", nil))
	if rec.Code != 503 || len(order) != 5 {
		t.Fatal("종료 뒤 미들웨어와 native 업그레이드를 실행하면 안 됩니다")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownWaitsForNativePostProcessing(t *testing.T) {
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requestContext context.Context
	s := newConfiguredAdapter(t, adapter.Config{Execute: func(scope adapter.HTTPContext) error { return scope.Writer().WriteStatus(204) }}, WithMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestContext = r.Context()
			next.ServeHTTP(w, r)
			close(entered)
			<-release
		})
	}))
	go func() {
		s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		close(returned)
	}()
	waitLifecycleSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.Canceled) || requestContext.Err() != nil {
		t.Fatalf("native 후처리와 요청 context를 보존해야 합니다: %v", err)
	}
	close(release)
	waitLifecycleSignal(t, returned)
	if err := s.Shutdown(context.Background()); err != nil || requestContext.Err() != context.Canceled {
		t.Fatalf("후처리 완료 뒤 다시 기다릴 수 있어야 합니다: %v", err)
	}
}

type failingWriter struct{ http.ResponseWriter }

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputCommitAndObservableErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &responseWriter{native: rec}
	if w.IsCommitted() {
		t.Fatal("쓰기 전 응답을 확정하면 안 됩니다")
	}
	w.AddHeader("X-ID", "one")
	w.AddHeader("X-ID", "two")
	value := json.RawMessage(`{"ok":true}`)
	if err := w.WriteJSON(201, value); err != nil || !w.IsCommitted() {
		t.Fatalf("JSON 출력: %v", err)
	}
	value[0] = 'x'
	if rec.Body.String() != `{"ok":true}` || len(rec.Header().Values("X-ID")) != 2 {
		t.Fatal("출력 bytes의 재사용과 반복 헤더를 보존해야 합니다")
	}
	w = &responseWriter{native: failingWriter{httptest.NewRecorder()}}
	if err := w.WriteString(200, "body"); !errors.Is(err, io.ErrClosedPipe) || !w.IsCommitted() {
		t.Fatalf("관측 가능한 출력 오류와 논리적 확정을 구분해야 합니다: %v", err)
	}
}
