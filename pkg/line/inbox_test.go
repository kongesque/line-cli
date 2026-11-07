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

func TestChatInvitationRequestsMatchChrome(t *testing.T) {
	for name, call := range map[string]func(*Client) error{
		"acceptChatInvitation": func(c *Client) error { return c.AcceptChatInvitationContext(context.Background(), 42, "c-group") },
		"rejectChatInvitation": func(c *Client) error { return c.RejectChatInvitationContext(context.Background(), 42, "c-group") },
	} {
		c, path, payload := inboxClient(t, 200, `{"code":0,"message":"OK","data":{}}`)
		if err := call(c); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(*path, "/TalkService/"+name) || *payload != `[{"reqSeq":42,"chatMid":"c-group"}]` {
			t.Fatalf("%s request %s %s", name, *path, *payload)
		}
		for _, body := range []string{`{"message":"OK"}`, `{"code":10,"message":"synthetic"}`, `[]`} {
			c, _, _ := inboxClient(t, 200, body)
			if err := call(c); err == nil {
				t.Fatalf("%s accepted %s", name, body)
			}
		}
	}
}

func TestGetAllChatMidsContextEnvelope(t *testing.T) {
	c, path, payload := inboxClient(t, 200, `{"code":0,"data":{"invitedChatMids":["c-one","c-two"]}}`)
	result, err := c.GetAllChatMidsContext(context.Background(), false, true)
	if err != nil || len(result.InvitedChatMids) != 2 || result.MemberChatMids != nil {
		t.Fatalf("result %+v %v", result, err)
	}
	if !strings.HasSuffix(*path, "/TalkService/getAllChatMids") || *payload != `[{"withMemberChats":false,"withInvitedChats":true},2]` {
		t.Fatalf("request %s %s", *path, *payload)
	}
	c, _, _ = inboxClient(t, 200, `{"code":0,"data":{}}`)
	if result, err := c.GetAllChatMidsContext(context.Background(), false, true); err != nil || len(result.InvitedChatMids) != 0 {
		t.Fatalf("omitted list: %+v %v", result, err)
	}
	for _, body := range []string{`{"code":0}`, `{"data":{}}`, `{"code":1,"message":"private detail","data":{}}`, `nope`} {
		c, _, _ := inboxClient(t, 200, body)
		_, err := c.GetAllChatMidsContext(context.Background(), false, true)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("accepted or leaked %s: %v", body, err)
		}
	}
}
