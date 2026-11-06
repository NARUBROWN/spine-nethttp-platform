package platform_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NARUBROWN/spine"
	platform "github.com/NARUBROWN/spine-nethttp-platform"
	"github.com/NARUBROWN/spine/pkg/boot"
	"github.com/NARUBROWN/spine/pkg/httpx"
)

type helloController struct{}

func (*helloController) Hello() httpx.Response[string] { return httpx.Response[string]{Body: "hello"} }

func TestPublicStarterAndPreparedHandler(t *testing.T) {
	app := platform.New()
	app.Constructor(func() *helloController { return &helloController{} })
	app.Route("GET", "/hello", (*helloController).Hello)
	p, err := app.Prepare(t.Context(), boot.Options{HTTP: &boot.HTTPOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	handler, err := spine.NetHTTPHandler(p)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hello", nil))
	if response.Code != 200 || response.Body.String() != "hello" {
		t.Fatalf("공개 앱 진입점: %d %q", response.Code, response.Body.String())
	}
}
