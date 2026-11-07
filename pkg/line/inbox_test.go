package line

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// inboxClient records one request and answers with a synthetic body.
func inboxClient(t *testing.T, status int, body string) (*Client, *string, *string) {
	t.Helper()
	var path, payload string
	c := NewClient("synthetic")
	c.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if path != "" {
			t.Fatal("mutation was sent more than once")
		}
		data, _ := io.ReadAll(r.Body)
		path, payload = r.URL.Path, string(data)
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(body)), Request: r}, nil
	})
	return c, &path, &payload
}

func TestSendChatCheckedMatchesChromeArguments(t *testing.T) {
	c, path, payload := inboxClient(t, 200, `{"code":0,"message":"OK","data":{}}`)
	if err := c.SendChatCheckedContext(context.Background(), 42, "c-group", "123456789012345678"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(*path, "/TalkService/sendChatChecked") || *payload != `[42,"c-group","123456789012345678",0]` {
		t.Fatalf("request %s %s", *path, *payload)
	}
}

func TestSendChatCheckedRequiresExplicitSuccess(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
	}{
		{200, `{"message":"OK"}`},
		{200, `not json`},
		{200, `{"code":10051,"message":"synthetic"}`},
		{500, `{"code":0}`},
	} {
		c, _, _ := inboxClient(t, tt.status, tt.body)
		if err := c.SendChatChecked(7, "u-peer", "1"); err == nil {
			t.Fatalf("%d %s accepted", tt.status, tt.body)
		}
	}
}
