package platform

import (
	"encoding/json"
	"net/http"

	httpcontract "github.com/NARUBROWN/spine/contract/http"
)

type responseWriter struct {
	native    http.ResponseWriter
	committed bool
}

var _ httpcontract.ResponseWriter = (*responseWriter)(nil)

func (w *responseWriter) SetHeader(key, value string) { w.native.Header().Set(key, value) }
func (w *responseWriter) AddHeader(key, value string) { w.native.Header().Add(key, value) }
func (w *responseWriter) IsCommitted() bool           { return w.committed }

func (w *responseWriter) WriteStatus(status int) error {
	w.native.WriteHeader(status)
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.committed = true
	}
	return nil
}

func (w *responseWriter) WriteJSON(status int, body json.RawMessage) error {
	w.SetHeader("Content-Type", "application/json; charset=UTF-8")
	return w.WriteBytes(status, body)
}

func (w *responseWriter) WriteString(status int, body string) error {
	w.SetHeader("Content-Type", "text/plain; charset=UTF-8")
	return w.WriteBytes(status, []byte(body))
}

func (w *responseWriter) WriteBytes(status int, body []byte) error {
	if err := w.WriteStatus(status); err != nil {
		return err
	}
	// 1xx 뒤의 본문 쓰기도 net/http에서는 최종 응답을 확정합니다.
	w.committed = true
	_, err := w.native.Write(body)
	return err
}
