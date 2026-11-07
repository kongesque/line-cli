package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// The session guard stops mutations even when a caller skips the CLI gate.
func TestReadOnlySessionStopsMessageMutations(t *testing.T) {
	c, f, _, s := setup(t, true)
	a := &actionAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return a }
	c.Session.ReadOnly = true
	f.history = []*line.Message{{ID: "123", From: "u-self", To: "u-peer", DeliveredTime: "10"}}
	for name, call := range map[string]func() error{
		"send": func() error {
			_, err := c.SendMentionsWithOptionsContext(context.Background(), "u-peer", "hello", "", nil, SendOptions{AllowPlaintext: true})
			return err
		},
		"react": func() error {
			_, err := c.ReactContext(context.Background(), "u-peer", "123", "like", false)
			return err
		},
		"unsend": func() error { _, err := c.UnsendContext(context.Background(), "u-peer", "123"); return err },
	} {
		err := call()
		var mutation *MutationError
		var typed *fault.Error
		if !errors.Is(err, session.ErrReadOnly) || !errors.As(err, &mutation) || !errors.As(err, &typed) || typed.Code != "read_only" {
			t.Fatalf("%s: %v", name, err)
		}
		if mutation.Mutation.Attempted || mutation.Mutation.Outcome != fault.NotAttempted || mutation.Mutation.RequestSequence != nil {
			t.Fatalf("%s: claimed an attempt: %+v", name, mutation.Mutation)
		}
	}
	if f.sends != 0 || a.calls != 0 || s.state.LastReqSeq != 0 {
		t.Fatalf("mutation reached LINE: sends=%d actions=%d seq=%d", f.sends, a.calls, s.state.LastReqSeq)
	}
}
