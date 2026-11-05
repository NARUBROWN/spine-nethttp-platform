package platform

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	httpcontract "github.com/NARUBROWN/spine/contract/http"
)

type requestContext struct {
	ctx                 context.Context
	method, path, query string
	headers             http.Header
	body                io.Reader
	writer              httpcontract.ResponseWriter
}

var _ httpcontract.HTTPContext = (*requestContext)(nil)

func newContext(w http.ResponseWriter, r *http.Request, reader io.Reader) *requestContext {
	headers := r.Header.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for name := range headers {
		if strings.EqualFold(name, "Host") {
			delete(headers, name)
		}
	}
	if r.Host != "" {
		headers.Set("Host", r.Host)
	}
	return &requestContext{
		ctx: r.Context(), method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery,
		headers: headers, body: requestReader{reader}, writer: &responseWriter{native: w},
	}
}

func (c *requestContext) Context() context.Context            { return c.ctx }
func (c *requestContext) Method() string                      { return c.method }
func (c *requestContext) RawPath() string                     { return c.path }
func (c *requestContext) RawQuery() string                    { return c.query }
func (c *requestContext) Header(name string) string           { return c.headers.Get(name) }
func (c *requestContext) Headers() map[string][]string        { return c.headers.Clone() }
func (c *requestContext) Body() io.Reader                     { return c.body }
func (c *requestContext) Writer() httpcontract.ResponseWriter { return c.writer }

type requestReader struct{ source io.Reader }

func (r requestReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		err = &httpcontract.RequestError{Status: http.StatusRequestEntityTooLarge, Cause: err}
	}
	return n, err
}
