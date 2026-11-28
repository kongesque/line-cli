package line

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListenSSENonOKIncludesResponseBody(t *testing.T) {
	oldClient := sseHTTPClient
	t.Cleanup(func() {
		sseHTTPClient = oldClient
	})

	sseHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"V3_TOKEN_CLIENT_LOGGED_OUT"}}`,
				)),
			}, nil
		}),
	}

	err := NewClient("stale-token").ListenSSE(context.Background(), 0, func(event, data string) {})
	if err == nil {
		t.Fatal("expected SSE error")
	}
	if !strings.Contains(err.Error(), "SSE error: 401") {
		t.Fatalf("err = %v, want status code", err)
	}
	if !strings.Contains(err.Error(), "V3_TOKEN_CLIENT_LOGGED_OUT") {
		t.Fatalf("err = %v, want response body detail", err)
	}
}

func TestListenSSEIdleTimeout(t *testing.T) {
	oldClient := sseHTTPClient
	oldTimeout := sseIdleTimeout
	t.Cleanup(func() {
		sseHTTPClient = oldClient
		sseIdleTimeout = oldTimeout
	})

	sseIdleTimeout = 10 * time.Millisecond
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
	})

	sseHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       pr,
			}, nil
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := NewClient("stale-token").ListenSSE(ctx, 0, func(event, data string) {})
	if !errors.Is(err, ErrSSEIdleTimeout) {
		t.Fatalf("err = %v, want ErrSSEIdleTimeout", err)
	}
}

func TestListenSSEContextCancellationWithContinuousHeartbeats(t *testing.T) {
	oldClient := sseHTTPClient
	oldTimeout := sseIdleTimeout
	t.Cleanup(func() {
		sseHTTPClient = oldClient
		sseIdleTimeout = oldTimeout
	})

	sseIdleTimeout = time.Second
	pr, pw := io.Pipe()
	sseHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       pr,
			}, nil
		}),
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	listenResult := make(chan error, 1)
	go func() {
		listenResult <- NewClient("stale-token").ListenSSE(ctx, 0, func(event, data string) {})
	}()

	heartbeatsWritten := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for range 5 {
			if _, err := io.WriteString(pw, "event: ping\ndata: null\n\n"); err != nil {
				return
			}
		}
		close(heartbeatsWritten)
		<-ctx.Done()
		_ = pw.Close()
	}()

	select {
	case <-heartbeatsWritten:
	case <-time.After(time.Second):
		t.Fatal("SSE reader did not consume heartbeat frames")
	}
	cancel(errors.New("receive auth probe due"))

	select {
	case err := <-listenResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListenSSE did not stop after context cancellation")
	}
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat writer did not stop after context cancellation")
	}
}

func TestListenSSEEmitsOpenAndPayloadFreeEvents(t *testing.T) {
	oldClient := sseHTTPClient
	t.Cleanup(func() { sseHTTPClient = oldClient })

	stream := strings.Join([]string{
		"event: ping",
		"data: null",
		"",
		"event: ping",
		"",
		"event: reconnect",
		"",
		"event: reconnect",
		"data: null",
		"",
		"event: talkException",
		`data: {"code":119,"reason":"MUST_REFRESH_V3_TOKEN"}`,
		"",
		"event: operation",
		`data: {"revision":"1"}`,
		"",
		"",
	}, "\n")

	sseHTTPClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(stream)),
			}, nil
		}),
	}

	type sseFrame struct{ event, data string }
	var got []sseFrame
	err := NewClient("tok").ListenSSE(context.Background(), 0, func(event, data string) {
		got = append(got, sseFrame{event, data})
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	want := []sseFrame{
		{SSEEventOpen, ""},
		{SSEEventPing, ""},
		{SSEEventPing, ""},
		{SSEEventReconnect, ""},
		{SSEEventReconnect, ""},
		{SSEEventTalkException, `{"code":119,"reason":"MUST_REFRESH_V3_TOKEN"}`},
		{"operation", `{"revision":"1"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d frames %#v, want %d %#v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestNewSSETalkExceptionClassificationAndRedaction(t *testing.T) {
	refreshErr := NewSSETalkException(`{"code":119,"reason":"MUST_REFRESH_V3_TOKEN","secret":"leak-me"}`)
	if !IsRefreshRequired(refreshErr) || !IsAuthError(refreshErr) || IsLoggedOut(refreshErr) {
		t.Fatalf("refresh talkException misclassified: %v", refreshErr)
	}
	if strings.Contains(refreshErr.Error(), "leak-me") {
		t.Fatalf("talkException leaked body: %v", refreshErr)
	}

	logoutErr := NewSSETalkException(`{"code":8,"reason":"V3_TOKEN_CLIENT_LOGGED_OUT","secret":"leak-me"}`)
	if !IsLoggedOut(logoutErr) || !IsAuthError(logoutErr) {
		t.Fatalf("logout talkException misclassified: %v", logoutErr)
	}
	if strings.Contains(logoutErr.Error(), "leak-me") {
		t.Fatalf("logout talkException leaked body: %v", logoutErr)
	}

	wrappedErr := NewSSETalkException(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"AUTHENTICATION_FAILED"}}`)
	if !IsLoggedOut(wrappedErr) || !IsAuthError(wrappedErr) {
		t.Fatalf("already-wrapped talkException misclassified: %v", wrappedErr)
	}

	nonAuthErr := NewSSETalkException(`{"code":99,"reason":"temporary"}`)
	if IsAuthError(nonAuthErr) || IsLoggedOut(nonAuthErr) || IsRefreshRequired(nonAuthErr) {
		t.Fatalf("non-auth talkException misclassified as auth: %v", nonAuthErr)
	}

	plainErr := NewSSETalkException("raw-secret-text")
	if strings.Contains(plainErr.Error(), "raw-secret-text") {
		t.Fatalf("plain talkException leaked body: %v", plainErr)
	}
}
