package platform

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/NARUBROWN/spine/contract/http"
)

func TestServeSingleUseAndListenerOwnership(t *testing.T) {
	server := newTestAdapter(t, adapter.ServerOptions{})
	for _, listener := range []net.Listener{nil, (*net.TCPListener)(nil)} {
		if err := server.Serve(listener); !errors.Is(err, adapter.ErrInvalidState) {
			t.Fatalf("nil 리스너는 실행 이력을 바꾸지 않고 거부해야 합니다: %v", err)
		}
	}
	if err := server.Shutdown(nil); !errors.Is(err, adapter.ErrInvalidState) {
		t.Fatalf("nil 종료 context는 상태를 바꾸지 않고 거부해야 합니다: %v", err)
	}
	first, serveResult := serveTestAdapter(t, server)
	second := newObservedListener(t)
	duplicate := make(chan error, 1)
	go func() { duplicate <- server.Serve(second) }()
	if err := waitLifecycleError(t, duplicate); !errors.Is(err, adapter.ErrInvalidState) {
		t.Fatalf("중복 Serve는 첫 실행을 기다리지 않고 실패해야 합니다: %v", err)
	}
	if second.closeCalls.Load() != 1 || first.closeCalls.Load() != 0 {
		t.Fatal("두 번째 리스너만 닫고 첫 실행은 유지해야 합니다")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitLifecycleError(t, serveResult); !errors.Is(err, adapter.ErrServerClosed) {
		t.Fatalf("정상 종료의 Serve 결과가 다릅니다: %v", err)
	}
	if first.closeCalls.Load() != 1 {
		t.Fatalf("실제 서버가 소유한 리스너를 한 번 닫아야 합니다: %d", first.closeCalls.Load())
	}
}

func TestShutdownBeforeServeAndRepeatedShutdown(t *testing.T) {
	server := newTestAdapter(t, adapter.ServerOptions{})
	for range 2 {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	listener := newObservedListener(t)
	if err := server.Serve(listener); !errors.Is(err, adapter.ErrInvalidState) || listener.closeCalls.Load() != 1 {
		t.Fatalf("종료 뒤 실행을 거부하고 전달받은 리스너를 닫아야 합니다: %v", err)
	}
}

func TestShutdownDuringServeStartup(t *testing.T) {
	server := newTestAdapter(t, adapter.ServerOptions{})
	listener := newObservedListener(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// 실제 서버가 리스너를 등록한 뒤 Accept에 들어가기 전의 경합을 고정합니다.
	server.httpServer.BaseContext = func(net.Listener) context.Context {
		close(entered)
		<-release
		return context.Background()
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	t.Cleanup(func() {
		unblock()
		_ = listener.Listener.Close()
		_ = server.Shutdown(context.Background())
	})
	waitLifecycleSignal(t, entered)
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- server.Shutdown(context.Background()) }()
	// Shutdown은 Serve의 반환도 기다리므로 리스너 종료를 먼저 관찰하고 진입을 풉니다.
	waitLifecycleSignal(t, listener.closed)
	unblock()
	if err := waitLifecycleError(t, serveResult); !errors.Is(err, adapter.ErrServerClosed) {
		t.Fatalf("Serve 시작과 정상 종료의 경합 결과가 다릅니다: %v", err)
	}
	if err := waitLifecycleError(t, shutdownResult); err != nil {
		t.Fatal(err)
	}
	if listener.closeCalls.Load() != 1 {
		t.Fatal("경합 중에도 리스너 소유권을 유지해야 합니다")
	}
}

func TestShutdownDeadlineAndConcurrentRetry(t *testing.T) {
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := newConfiguredAdapter(t, adapter.Config{Execute: func(ctx adapter.HTTPContext) error {
		entered <- ctx.Context()
		<-release
		writer := ctx.Writer()
		return writer.WriteStatus(http.StatusNoContent)
	}})
	listener, serveResult := serveTestAdapter(t, server)
	t.Cleanup(unblock)
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	requestResult := make(chan error, 1)
	go func() {
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("unexpected HTTP status")
			}
		}
		requestResult <- err
	}()
	var requestContext context.Context
	select {
	case requestContext = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP 요청이 실행되지 않았습니다")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("활성 요청이 남으면 종료 deadline을 반환해야 합니다: %v", err)
	}
	if requestContext.Err() != nil {
		t.Fatalf("HTTP Shutdown이 활성 요청 context를 강제 취소하면 안 됩니다: %v", requestContext.Err())
	}
	if err := waitLifecycleError(t, serveResult); !errors.Is(err, adapter.ErrServerClosed) {
		t.Fatalf("리스너 종료는 활성 요청 완료와 구분해야 합니다: %v", err)
	}
	select {
	case err := <-requestResult:
		t.Fatalf("종료 deadline이 활성 요청을 강제 종료하면 안 됩니다: %v", err)
	default:
	}
	// 첫 timeout을 저장해 버리면 아래 재대기가 모두 같은 오류로 끝납니다.
	retries := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			retries <- server.Shutdown(ctx)
		}()
	}
	unblock()
	for range 2 {
		if err := waitLifecycleError(t, retries); err != nil {
			t.Fatalf("새 context의 동시 종료 대기는 성공해야 합니다: %v", err)
		}
	}
	if err := waitLifecycleError(t, requestResult); err != nil {
		t.Fatalf("남아 있던 HTTP 요청이 완료되어야 합니다: %v", err)
	}
}

func TestServeFailurePreservesCauseAndClosesListener(t *testing.T) {
	server := newTestAdapter(t, adapter.ServerOptions{})
	wantErr := errors.New("permanent accept failure")
	listener := &failingListener{err: wantErr}
	if err := server.Serve(listener); !errors.Is(err, wantErr) || listener.closeCalls.Load() != 1 {
		t.Fatalf("실행 오류 원인과 리스너 정리를 보존해야 합니다: %v", err)
	}
	second := &failingListener{err: wantErr}
	if err := server.Serve(second); !errors.Is(err, adapter.ErrInvalidState) || second.closeCalls.Load() != 1 {
		t.Fatalf("실행 실패 이후에도 같은 인스턴스를 재실행하면 안 됩니다: %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownBlocksEntriesWithoutClosingExternalServer(t *testing.T) {
	server := newConfiguredAdapter(t, adapter.Config{Execute: func(ctx adapter.HTTPContext) error {
		writer := ctx.Writer()
		return writer.WriteStatus(http.StatusNoContent)
	}})
	external := httptest.NewServer(server.Handler())
	defer external.Close()
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := external.Client().Get(external.URL)
	if err != nil {
		t.Fatalf("Handler를 실행하는 외부 서버의 종료 책임은 외부에 있습니다: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("외부 서버 연결은 유지하고 새 앱 진입은 503이어야 합니다: %d", response.StatusCode)
	}
}

type observedListener struct {
	net.Listener
	entered    chan struct{}
	closed     chan struct{}
	enterOnce  sync.Once
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func (l *observedListener) Accept() (net.Conn, error) {
	l.enterOnce.Do(func() { close(l.entered) })
	return l.Listener.Accept()
}

func (l *observedListener) Close() error {
	l.closeCalls.Add(1)
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.closed) })
	return err
}

func newObservedListener(t *testing.T) *observedListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return &observedListener{Listener: listener, entered: make(chan struct{}), closed: make(chan struct{})}
}

func serveTestAdapter(t *testing.T, server *NetHTTPAdapter) (*observedListener, <-chan error) {
	t.Helper()
	listener := newObservedListener(t)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { result <- server.Serve(listener); close(done) }()
	t.Cleanup(func() {
		_ = listener.Listener.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		waitLifecycleSignal(t, done)
	})
	waitLifecycleSignal(t, listener.entered)
	return listener, result
}

func waitLifecycleError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("실행 또는 종료가 제한 시간 안에 반환되지 않았습니다")
		return nil
	}
}

func waitLifecycleSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("실행 단계 신호를 받지 못했습니다")
	}
}

type failingListener struct {
	err        error
	closeCalls atomic.Int32
}

func (l *failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (*failingListener) Addr() net.Addr              { return &net.TCPAddr{} }
func (l *failingListener) Close() error              { l.closeCalls.Add(1); return nil }
