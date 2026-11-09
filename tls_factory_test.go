package platform

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	adapter "github.com/NARUBROWN/spine/contract/http"
)

func TestTLSConfigFailsBeforeMiddlewareConstruction(t *testing.T) {
	calls := 0
	factory := NewFactory(WithMiddleware(func(next http.Handler) http.Handler { calls++; return next }))
	_, err := factory.New(adapter.Config{Execute: func(adapter.HTTPContext) error { return nil }, Serve: adapter.ServeOptions{TLS: &tls.Config{}}})
	if !errors.Is(err, adapter.ErrInvalidAdapter) || calls != 0 {
		t.Fatalf("TLS 검증 전 native 초기화: %v calls=%d", err, calls)
	}
}
