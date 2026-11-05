// Package platform은 Spine HTTP 계약의 net/http 구현을 제공합니다.
package platform

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"

	httpcontract "github.com/NARUBROWN/spine/contract/http"
)

type factory struct {
	middleware []func(http.Handler) http.Handler
}

// NetHTTPOption은 net/http 전용 구성을 설정합니다.
type NetHTTPOption func(*factory)

// WithMiddleware는 먼저 전달한 미들웨어를 바깥에 배치합니다.
// 생성 함수는 검증이 끝난 뒤 준비마다 한 번 실행하며 nil 결과는 거부합니다.
func WithMiddleware(middleware ...func(http.Handler) http.Handler) NetHTTPOption {
	copy := slices.Clone(middleware)
	return func(f *factory) { f.middleware = append(f.middleware, copy...) }
}

// NewFactory는 리스너나 실행 고루틴 없이 HTTP 플랫폼을 구성합니다.
func NewFactory(options ...NetHTTPOption) httpcontract.Factory {
	f := factory{}
	for _, option := range options {
		if option != nil {
			option(&f)
		}
	}
	f.middleware = slices.Clone(f.middleware)
	return f
}

var _ httpcontract.NetHTTPUpgradeFactory = factory{}

func (factory) Capabilities() httpcontract.Capabilities {
	return httpcontract.Capabilities{
		ContractVersion: httpcontract.ContractVersion,
		NetHTTPHandler:  true, NetHTTPUpgrade: true, ClientDisconnectCancellation: true,
		// net/http 서버 자체의 패닉 복구는 비활성화할 수 없습니다.
		ServerOptions: httpcontract.ServerOptionSupport{
			ReadHeaderTimeout: true, ReadTimeout: true, WriteTimeout: true,
			IdleTimeout: true, MaxHeaderBytes: true,
		},
	}
}

func (f factory) New(config httpcontract.Config) (httpcontract.HTTPAdapter, error) {
	return f.NewWithNetHTTPUpgrades(config, nil)
}

func (f factory) NewWithNetHTTPUpgrades(config httpcontract.Config, routes []httpcontract.NetHTTPUpgradeRoute) (httpcontract.HTTPAdapter, error) {
	if err := config.Validate(f.Capabilities()); err != nil {
		return nil, err
	}
	if err := httpcontract.ValidateNetHTTPUpgradeRoutes(routes); err != nil {
		return nil, err
	}
	for i, middleware := range f.middleware {
		if middleware == nil {
			return nil, fmt.Errorf("%w: middleware[%d] is nil", httpcontract.ErrInvalidAdapter, i)
		}
	}
	upgrades := make(map[string]http.Handler, len(routes))
	for _, route := range routes {
		if _, exists := upgrades[route.Path]; exists {
			return nil, fmt.Errorf("%w: duplicate upgrade path %q", httpcontract.ErrInvalidAdapter, route.Path)
		}
		upgrades[route.Path] = route.Handler
	}
	server := newAdapter(config)
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upgrade := upgrades[r.URL.Path]; r.Method == http.MethodGet && upgrade != nil {
			upgrade.ServeHTTP(w, r)
			return
		}
		server.executeRequest(w, r)
	})
	for _, middleware := range slices.Backward(f.middleware) {
		handler = middleware(handler)
		if isNil(handler) {
			return nil, fmt.Errorf("%w: middleware returned a nil handler", httpcontract.ErrInvalidAdapter)
		}
	}
	server.native = handler
	return server, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
