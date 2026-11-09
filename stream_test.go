package platform

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	httpcontract "github.com/NARUBROWN/spine/contract/http"
)

func TestNativeTruncatedBodyNeverReportsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
		broken      bool
	}{
		{"content-length", "Content-Length: 5\r\n\r\n{}", true},
		{"chunk-without-terminator", "Transfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n", true},
		{"chunk-without-trailer-end", "Transfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n", true},
		{"complete-chunk", "Transfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readDone := make(chan error, 1)
			s := newConfiguredAdapter(t, httpcontract.Config{Execute: func(scope httpcontract.HTTPContext) error {
				_, err := io.ReadAll(scope.Body())
				readDone <- err
				return scope.Writer().WriteStatus(204)
			}})
			listener, _ := serveTestAdapter(t, s)
			conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n"+tc.frame); err != nil {
				t.Fatal(err)
			}
			if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			readErr := waitLifecycleError(t, readDone)
			// net/http는 trailer 잘림에 별도의 native 오류를 반환합니다.
			// 성공이나 정상 EOF로 바꾸지 않고 원래 읽기 실패를 전달해야 합니다.
			if tc.broken && (readErr == nil || errors.Is(readErr, io.EOF)) || !tc.broken && readErr != nil {
				t.Fatalf("native EOF 검증: %v", readErr)
			}
		})
	}
}

func TestClientDisconnectCancelsScope(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	s := newConfiguredAdapter(t, httpcontract.Config{Execute: func(scope httpcontract.HTTPContext) error {
		close(entered)
		<-scope.Context().Done()
		close(canceled)
		return scope.Context().Err()
	}})
	listener, _ := serveTestAdapter(t, s)
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	waitLifecycleSignal(t, entered)
	_ = conn.Close()
	waitLifecycleSignal(t, canceled)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
