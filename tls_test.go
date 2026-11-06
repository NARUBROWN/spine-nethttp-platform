package platform

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	adapter "github.com/NARUBROWN/spine/contract/http"
)

func TestTLSOptionErrorsCloseListenerWithoutConsumingServe(t *testing.T) {
	server := newTestAdapter(t, adapter.ServerOptions{})
	for _, option := range []adapter.ServeOption{adapter.WithTLS(&tls.Config{}), adapter.WithTLS(nil), nil} {
		listener := newObservedListener(t)
		if err := server.Serve(listener, option); !errors.Is(err, adapter.ErrInvalidAdapter) {
			t.Fatalf("TLS 실행 옵션 오류: %v", err)
		}
		if listener.closeCalls.Load() != 1 {
			t.Fatalf("실패한 실행의 리스너 정리: %d", listener.closeCalls.Load())
		}
		select {
		case <-listener.entered:
			t.Fatal("잘못된 TLS 옵션으로 Accept를 시작했습니다")
		default:
		}
	}
	_, result := serveTestAdapter(t, server)
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := waitLifecycleError(t, result); err != adapter.ErrServerClosed {
		t.Fatalf("유효한 재시도와 정상 종료: %v", err)
	}
}
