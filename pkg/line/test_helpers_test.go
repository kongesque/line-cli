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

// Shared synthetic fixtures for package tests.
func testRPCContextCancellation(t *testing.T, method string, call func(*Client, context.Context) error) {
	t.Helper()
	requestStarted := make(chan struct{})
	client := NewClient("valid-token")
	client.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(requestStarted)
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- call(client, ctx)
	}()

	select {
	case <-requestStarted:
	// The first signed request initializes crypto, which can take longer under
	// the race detector on a busy runner. Cancellation itself stays bounded below.
	case <-time.After(10 * time.Second):
		t.Fatalf("%s request did not start", method)
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s did not stop after context cancellation", method)
	}
}

func qrTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newReactionTestClient(t *testing.T, wantPath string, body *string) *Client {
	return newReactionTestClientWithResponse(t, wantPath, `{"code":0,"message":"ok","data":null}`, body)
}

func newReactionTestClientWithResponse(t *testing.T, wantPath, responseBody string, body *string) *Client {
	t.Helper()
	client := NewClient("test-token")
	client.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != wantPath {
				t.Fatalf("path = %q, want %q", req.URL.Path, wantPath)
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if body != nil {
				*body = string(data)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}, nil
		}),
	}
	return client
}
