package line

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestOlderHistoryRequestPreservesAnchor(t *testing.T) {
	var body string
	c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/getPreviousMessagesV2WithRequest", `{"code":0,"data":[{"id":"9007199254740993","createdTime":"5","deliveredTime":"9007199254740995"}]}`, &body)
	anchor := MessageIDWrapper{MessageID: "9007199254740997", DeliveredTime: json.Number("9007199254740996")}
	messages, err := c.GetPreviousMessagesV2Context(context.Background(), "u-chat", anchor, 50)
	if err != nil {
		t.Fatal(err)
	}
	var args []json.RawMessage
	if err := json.Unmarshal([]byte(body), &args); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Chat   string           `json:"messageBoxId"`
		Anchor MessageIDWrapper `json:"endMessageId"`
		Count  int              `json:"messagesCount"`
	}
	if len(args) != 2 {
		t.Fatalf("args: %s", body)
	}
	if err := json.Unmarshal(args[0], &request); err != nil {
		t.Fatal(err)
	}
	if request.Chat != "u-chat" || request.Anchor != anchor || request.Count != 50 || string(args[1]) != "1" {
		t.Fatalf("request: %s", body)
	}
	if messages[0].ID != "9007199254740993" || messages[0].DeliveredTime != "9007199254740995" || messages[0].CreatedTime != "5" {
		t.Fatal("lost exact message fields")
	}
}

func TestOlderHistoryRejectsMalformedResponses(t *testing.T) {
	for _, response := range []string{`{}`, `{"code":0}`, `{"code":0,"data":null}`, `{"code":0,"data":{}}`, `{"code":4,"message":"private-body"}`, `{"code":0,"data":[{"id":9007199254740993}]}`} {
		c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/getPreviousMessagesV2WithRequest", response, nil)
		_, err := c.GetPreviousMessagesV2Context(context.Background(), "u-chat", MessageIDWrapper{MessageID: "123", DeliveredTime: "5"}, 50)
		if err == nil || strings.Contains(err.Error(), "private-body") {
			t.Fatalf("response accepted/leaked: %v", err)
		}
	}
}

func TestOlderHistoryCancellation(t *testing.T) {
	testRPCContextCancellation(t, "older history", func(c *Client, ctx context.Context) error {
		_, err := c.GetPreviousMessagesV2Context(ctx, "u-chat", MessageIDWrapper{MessageID: "123", DeliveredTime: "5"}, 50)
		return err
	})
}
