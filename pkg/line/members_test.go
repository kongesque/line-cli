package line

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGetChatsMembershipRequest(t *testing.T) {
	var body string
	c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/getChats", `{"code":0,"data":{"chats":[{"chatMid":"c-group","extra":{"groupExtra":{"memberMids":{"u-self":"123"},"inviteeMids":{}}}}]}}`, &body)
	got, err := c.GetChatsContext(context.Background(), []string{"c-group"}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	var args []json.RawMessage
	if json.Unmarshal([]byte(body), &args) != nil || len(args) != 2 {
		t.Fatal("invalid argument envelope")
	}
	var req GetChatsRequest
	if json.Unmarshal(args[0], &req) != nil || len(req.ChatMids) != 1 || req.ChatMids[0] != "c-group" || !req.WithMembers || !req.WithInvitees || string(args[1]) != "2" {
		t.Fatal("incorrect membership request")
	}
	group := got.Chats[0].Extra.GroupExtra
	if !group.MemberMids["u-self"] || group.InviteeMids == nil || len(group.InviteeMids) != 0 {
		t.Fatal("membership presence lost")
	}
}

func TestGetChatsRejectsMissingOrMalformedPayload(t *testing.T) {
	for _, raw := range []string{`{}`, `{"data":{"chats":[]}}`, `{"code":0}`, `{"code":0,"data":null}`, `{"code":0,"data":{}}`, `{"code":4,"message":"private-body"}`, `{"code":0,"data":{"chats":[{"extra":{"groupExtra":{"memberMids":"private-body"}}}]}}`} {
		c := newReactionTestClientWithResponse(t, "/api/talk/thrift/Talk/TalkService/getChats", raw, nil)
		if _, err := c.GetChats([]string{"c-group"}, true, true); err == nil || strings.Contains(err.Error(), "private-body") {
			t.Fatalf("accepted/leaked invalid response: %v", err)
		}
	}
}
