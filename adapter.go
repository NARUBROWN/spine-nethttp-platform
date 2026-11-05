package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	httpcontract "github.com/NARUBROWN/spine/contract/http"
)

type lifecycleState uint8

const (
	stateReady lifecycleState = iota
	stateServing
	stateStopping
	stateStopped
)

// NetHTTPAdapter는 관리 서버와 외부 서버에 동일한 요청 경계를 제공합니다.
// NewFactory로 생성하며 외부 서버의 종료는 호출자가 관리합니다.
type NetHTTPAdapter struct {
	execute      httpcontract.ExecuteFunc
	native       http.Handler
	httpServer   *http.Server
	maxBodyBytes int64
	mu           sync.Mutex
	state        lifecycleState
	active       int
	drained      chan struct{}
	shutdownDone chan struct{}
	shutdownOnce sync.Once
	shutdownErr  error
}

var _ httpcontract.HTTPAdapter = (*NetHTTPAdapter)(nil)
var _ httpcontract.NetHTTPHandlerProvider = (*NetHTTPAdapter)(nil)

func newAdapter(config httpcontract.Config) *NetHTTPAdapter {
	opts := config.Options
	if opts.ReadHeaderTimeout == 0 {
		opts.ReadHeaderTimeout = 5 * time.Second
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 30 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 30 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 120 * time.Second
	}
	if opts.MaxHeaderBytes == 0 {
		opts.MaxHeaderBytes = 1 << 20
	}
	if opts.MaxBodyBytes == 0 {
		opts.MaxBodyBytes = 32 << 20
	}
	s := &NetHTTPAdapter{
		execute: config.Execute, maxBodyBytes: opts.MaxBodyBytes,
		drained: make(chan struct{}), shutdownDone: make(chan struct{}),
	}
	s.httpServer = &http.Server{
		Handler: s, ReadHeaderTimeout: opts.ReadHeaderTimeout, ReadTimeout: opts.ReadTimeout,
		WriteTimeout: opts.WriteTimeout, IdleTimeout: opts.IdleTimeout, MaxHeaderBytes: opts.MaxHeaderBytes,
	}
	return s
}

// Handler는 초기화 없이 동일한 보호된 진입점을 반환합니다.
func (s *NetHTTPAdapter) Handler() http.Handler {
	if s == nil || s.native == nil {
		return nil
	}
	return s
}

// Serve는 유효한 리스너를 소유하며 인스턴스당 한 번만 실행합니다.
func (s *NetHTTPAdapter) Serve(listener net.Listener) error {
	if isNil(listener) {
		return fmt.Errorf("%w: listener is nil", httpcontract.ErrInvalidState)
	}
	if s == nil || s.httpServer == nil || s.native == nil {
		return errors.Join(fmt.Errorf("%w: NetHTTPAdapter is not initialized", httpcontract.ErrInvalidAdapter), listener.Close())
	}
	s.mu.Lock()
	if s.state != stateReady {
		s.mu.Unlock()
		return errors.Join(fmt.Errorf("%w: Serve requires a new adapter", httpcontract.ErrInvalidState), listener.Close())
	}
	s.state = stateServing
	s.mu.Unlock()
	// net/http.Server.Serve는 모든 반환 경로에서 전달받은 리스너를 닫습니다.
	err := s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return httpcontract.ErrServerClosed
	}
	if err == nil {
		return fmt.Errorf("%w: Serve returned without an error", httpcontract.ErrInvalidAdapter)
	}
	return err
}

// ServeHTTP의 바깥 경계는 native 미들웨어와 업그레이드의 반환까지 추적합니다.
func (s *NetHTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state >= stateStopping {
		s.mu.Unlock()
		http.Error(w, "application is closing", http.StatusServiceUnavailable)
		return
	}
	s.active++
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer func() {
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.active--
		if s.state >= stateStopping && s.active == 0 {
			close(s.drained)
		}
	}()
	s.native.ServeHTTP(w, r.WithContext(ctx))
}

func (s *NetHTTPAdapter) executeRequest(w http.ResponseWriter, r *http.Request) {
	reader := r.Body
	if reader == nil {
		reader = http.NoBody
	}
	if s.maxBodyBytes > 0 {
		reader = http.MaxBytesReader(w, reader, s.maxBodyBytes)
	}
	// Execute가 응답을 처리하므로 반환 오류로 두 번째 응답을 작성하지 않습니다.
	_ = s.execute(newContext(w, r, reader))
}

// StopAccepting은 활성 요청을 보존하고 모든 새 native 진입을 동기 차단합니다.
func (s *NetHTTPAdapter) StopAccepting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state >= stateStopping {
		return
	}
	s.state = stateStopping
	if s.active == 0 {
		close(s.drained)
	}
}

// Shutdown의 context는 완료 대기만 제한하며 정리는 한 번 시작해 계속합니다.
func (s *NetHTTPAdapter) Shutdown(ctx context.Context) error {
	if isNil(ctx) {
		return fmt.Errorf("%w: shutdown context is nil", httpcontract.ErrInvalidState)
	}
	if s == nil || s.httpServer == nil {
		return fmt.Errorf("%w: NetHTTPAdapter is not initialized", httpcontract.ErrInvalidAdapter)
	}
	s.StopAccepting()
	s.shutdownOnce.Do(func() {
		go func() {
			s.shutdownErr = s.httpServer.Shutdown(context.Background())
			<-s.drained
			s.mu.Lock()
			s.state = stateStopped
			s.mu.Unlock()
			close(s.shutdownDone)
		}()
	})
	select {
	case <-s.shutdownDone:
		return s.shutdownErr
	default:
	}
	select {
	case <-s.shutdownDone:
		return s.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
