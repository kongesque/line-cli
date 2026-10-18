package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type actionAPI struct {
	*fakeAPI
	calls    int
	action   string
	reaction int
	err      error
}

func (f *actionAPI) perform(seq int64, action string) error {
	if seq != f.store.state.LastReqSeq {
		panic("mutation before persistent sequence")
	}
	f.calls++
	f.action = action
	return f.err
}
func (f *actionAPI) React(seq int64, _ string, r line.ReactionType) error {
	f.reaction = r.PredefinedReactionType
	return f.perform(seq, "react")
}
func (f *actionAPI) CancelReaction(seq int64, _ string) error { return f.perform(seq, "remove") }
func (f *actionAPI) UnsendMessage(seq int64, _ string) error  { return f.perform(seq, "unsend") }

func TestReactionAndUnsendAreScopedAndNeverRetried(t *testing.T) {
	c, f, _, _ := setup(t, true)
	a := &actionAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return a }
	f.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self", DeliveredTime: "10"}, {ID: "124", From: "u-self", To: "u-peer", DeliveredTime: "11"}}
	if _, err := c.Unsend("u-peer", "123"); err == nil || a.calls != 0 {
		t.Fatal("unsent another person's message")
	}
	if _, err := c.React("u-peer", "999", "like", false); err == nil || a.calls != 0 {
		t.Fatal("reacted to unverified message")
	}
	if _, err := c.React("u-peer", "123", "❤️", false); err != nil || a.reaction != 3 || a.calls != 1 {
		t.Fatal("reaction failed", err)
	}
	if _, err := c.React("u-peer", "123", "", true); err != nil || a.action != "remove" {
		t.Fatal("remove failed", err)
	}
	if _, err := c.Unsend("u-peer", "124"); err != nil || a.action != "unsend" {
		t.Fatal("own unsend failed", err)
	}
	a.err = errors.New("private server body")
	before := a.calls
	if _, err := c.Unsend("u-peer", "124"); err == nil || strings.Contains(err.Error(), "private") || a.calls != before+1 {
		t.Fatal("unsafe retry/error", err)
	}
}

func TestReplyPreservesEncryptionAndRelationFields(t *testing.T) {
	for _, plain := range []bool{true, false} {
		c, f, _, _ := setup(t, plain)
		f.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self"}}
		result, err := c.SendMentionsWithOptionsContext(context.Background(), "u-peer", "reply", "123", nil, SendOptions{AllowPlaintext: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.Encrypted == plain || f.sent.RelatedMessageID != "123" || f.sent.MessageRelationType != 3 || f.sent.RelatedMessageServiceCode != 1 {
			t.Fatal("invalid reply envelope")
		}
		f.sendErr = errors.New("NOT_FOUND")
		_, err = c.SendMentionsWithOptionsContext(context.Background(), "u-peer", "reply", "123", nil, SendOptions{AllowPlaintext: true})
		if err == nil || f.sends != 2 || f.sent.RelatedMessageID != "123" {
			t.Fatal("reply retried without relation")
		}
	}
}

func TestActionValidationBeforeMutation(t *testing.T) {
	for _, id := range []string{"", "local-1", "+123", "0", "-1", "../../123"} {
		if ValidateMessageID(id) == nil {
			t.Fatal("accepted invalid ID", id)
		}
	}
	if _, err := ReactionType("arbitrary"); err == nil {
		t.Fatal("accepted unsupported reaction")
	}
	c, f, _, _ := setup(t, true)
	if _, err := c.SendReply("u-peer", "hello", "invalid"); err == nil || f.sends != 0 {
		t.Fatal("invalid reply sent")
	}
}

func (f *actionAPI) ReactContext(_ context.Context, seq int64, arg1 string, r line.ReactionType) error {
	return f.React(seq, arg1, r)
}

func (f *actionAPI) CancelReactionContext(_ context.Context, seq int64, arg1 string) error {
	return f.CancelReaction(seq, arg1)
}

func (f *actionAPI) UnsendMessageContext(_ context.Context, seq int64, arg1 string) error {
	return f.UnsendMessage(seq, arg1)
}
