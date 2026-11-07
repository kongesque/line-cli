package messaging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type readAPI struct {
	*fakeAPI
	calls          int
	chat, message  string
	seq            int64
	err            error
	historyLimit   int
	decryptAttempt bool
}

func (f *readAPI) SendChatCheckedContext(_ context.Context, seq int64, chat, message string) error {
	if seq != f.store.state.LastReqSeq || seq == 0 {
		panic("read receipt before persistent sequence")
	}
	f.calls++
	f.seq, f.chat, f.message = seq, chat, message
	return f.err
}

func (f *readAPI) GetRecentMessagesV2Context(_ context.Context, chat string, limit int) ([]*line.Message, error) {
	f.historyLimit = limit
	return f.history, nil
}

// Key lookups would mean the read path decrypts history; it must not.
func (f *readAPI) GetE2EEPublicKeyContext(context.Context, string, int, int) (*line.E2EEPublicKey, error) {
	f.decryptAttempt = true
	return nil, errors.New("unexpected key lookup")
}

func readSetup(t *testing.T) (*Client, *readAPI, *memStore) {
	t.Helper()
	c, f, _, s := setup(t, false)
	r := &readAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return r }
	return c, r, s
}

func TestMarkReadPicksNewestMessageFromSomeoneElse(t *testing.T) {
	c, r, s := readSetup(t)
	r.history = []*line.Message{
		{ID: "900", From: "u-self", To: "u-peer"}, // own: never marked
		{ID: "800", From: "u-peer", To: "u-self", ContentMetadata: map[string]string{"SKIP_BADGE_COUNT": "true"}},
		{ID: "18446744073709551000", From: "u-other", To: "u-self"}, // another chat
		{ID: "700", From: "u-peer", To: "u-self", Chunks: []string{"a", "b", "c", "d", "e"}},
		{ID: "99", From: "u-peer", To: "u-self"},
		{ID: "local-5", From: "u-peer", To: "u-self"},
	}
	result, err := c.MarkReadContext(context.Background(), "u-peer", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || r.chat != "u-peer" || r.message != "700" || r.historyLimit != ReadScanCount || r.decryptAttempt {
		t.Fatalf("wrong receipt: %+v", r)
	}
	if result.Action != "read" || result.ChatID != "u-peer" || result.MessageID != "700" || result.RequestSequence != s.state.LastReqSeq {
		t.Fatalf("result %+v", result)
	}
}

func TestMarkReadGroupUsesGroupMessages(t *testing.T) {
	c, r, _ := readSetup(t)
	group := "c-group"
	r.history = []*line.Message{
		{ID: "12", From: "u-self", To: group, ToType: 2},
		{ID: "11", From: "u-member", To: group, ToType: 2},
		{ID: "13", From: "u-member", To: "c-elsewhere", ToType: 2},
	}
	if _, err := c.MarkReadContext(context.Background(), group, ""); err != nil || r.message != "11" {
		t.Fatalf("group receipt %q: %v", r.message, err)
	}
}

func TestMarkReadExplicitMessageIsVerified(t *testing.T) {
	c, r, _ := readSetup(t)
	r.history = []*line.Message{{ID: "124", From: "u-self", To: "u-peer", DeliveredTime: "11"}, {ID: "123", From: "u-peer", To: "u-self", DeliveredTime: "10"}}
	if _, err := c.MarkReadContext(context.Background(), "u-peer", "123"); err != nil || r.message != "123" || r.calls != 1 {
		t.Fatalf("explicit receipt %q: %v", r.message, err)
	}
	var typed *fault.Error
	if _, err := c.MarkReadContext(context.Background(), "u-peer", "124"); !errors.As(err, &typed) || typed.Code != "invalid_arguments" || r.calls != 1 {
		t.Fatalf("own message accepted: %v", err)
	}
	if _, err := c.MarkReadContext(context.Background(), "u-peer", "555"); !errors.As(err, &typed) || typed.Code != "message_not_found" || r.calls != 1 {
		t.Fatalf("missing message accepted: %v", err)
	}
	for _, bad := range []string{"local-1", "abc", "0"} {
		if _, err := c.MarkReadContext(context.Background(), "u-peer", bad); err == nil || r.calls != 1 {
			t.Fatalf("invalid ID %q accepted", bad)
		}
	}
	if _, err := c.MarkReadContext(context.Background(), "not a chat", ""); err == nil || r.calls != 1 {
		t.Fatal("invalid chat accepted")
	}
}

func TestMarkReadNothingToMark(t *testing.T) {
	c, r, s := readSetup(t)
	r.history = []*line.Message{{ID: "5", From: "u-self", To: "u-peer"}}
	var typed *fault.Error
	if _, err := c.MarkReadContext(context.Background(), "u-peer", ""); !errors.As(err, &typed) || typed.Code != "message_not_found" || typed.Stage != "lookup" {
		t.Fatalf("got %v", err)
	}
	if r.calls != 0 || s.state.LastReqSeq != 0 {
		t.Fatal("nothing to mark still reserved a sequence or sent a receipt")
	}
}

func TestMarkReadOutcomesAreNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		outcome fault.Outcome
	}{
		{"lost response", io.ErrUnexpectedEOF, fault.Unknown},
		{"rejected", &line.ResponseError{Status: 403}, fault.Rejected},
		{"service failure", errors.New("synthetic private body"), fault.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r, s := readSetup(t)
			r.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self"}}
			r.err = tc.failure
			_, err := c.MarkReadContext(context.Background(), "u-peer", "")
			e := mutationDetails(t, err)
			if r.calls != 1 || e.Stage != "read" || e.Mutation.Action != "read" || e.Mutation.Outcome != tc.outcome || !e.Mutation.Attempted ||
				e.Mutation.ChatID != "u-peer" || e.Mutation.MessageID != "123" || e.Mutation.RequestSequence == nil || *e.Mutation.RequestSequence != s.state.LastReqSeq {
				t.Fatalf("bad outcome: calls=%d %+v", r.calls, e)
			}
		})
	}
}

func TestMarkReadDeadlineBeforeDispatch(t *testing.T) {
	c, r, s := readSetup(t)
	r.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self"}}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := c.MarkReadContext(ctx, "u-peer", ""); !errors.Is(err, context.DeadlineExceeded) || r.calls != 0 || s.state.LastReqSeq != 0 {
		t.Fatalf("expired deadline: %v calls=%d", err, r.calls)
	}
}

func TestMarkReadSequenceFailureNeverSends(t *testing.T) {
	c, r, s := readSetup(t)
	r.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self"}}
	s.saveErr = session.ErrDurabilityUncertain
	_, err := c.MarkReadContext(context.Background(), "u-peer", "")
	e := mutationDetails(t, err)
	if e.Stage != "persist_sequence" || e.Mutation.Attempted || e.Mutation.Outcome != fault.NotAttempted || r.calls != 0 {
		t.Fatalf("bad outcome: %+v", e)
	}
}

type pagedReadAPI struct {
	*historyAPI
	calls int
}

func (f *pagedReadAPI) SendChatCheckedContext(context.Context, int64, string, string) error {
	f.calls++
	return nil
}

func TestMarkReadLookupBudgetSendsNothing(t *testing.T) {
	c, api := historySetup(t)
	r := &pagedReadAPI{historyAPI: api}
	c.Session.NewClient = func(string) session.API { return r }
	api.history = []*line.Message{historyMessage("100")}
	for i := 99; i > 80; i-- {
		api.pages = append(api.pages, []*line.Message{historyMessage(fmt.Sprint(i))})
	}
	_, err := c.MarkReadContext(context.Background(), "u-peer", "1")
	requireCode(t, err, "lookup_incomplete")
	if r.calls != 0 {
		t.Fatal("incomplete lookup sent a read receipt")
	}
}
