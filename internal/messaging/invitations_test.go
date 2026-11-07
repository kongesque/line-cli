package messaging

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

var (
	invitedGroup = "c" + strings.Repeat("A", 43)
	otherGroup   = "c" + strings.Repeat("B", 43)
)

// inviteAPI fails the test if invitation handling touches group keys.
type inviteAPI struct {
	session.API
	t        *testing.T
	store    *memStore
	invited  []string
	listErr  error
	lists    int
	accepts  []string
	rejects  []string
	seqs     []int64
	err      error
	response *line.GetAllChatMidsResponse
}

func (f *inviteAPI) GetAllChatMidsContext(_ context.Context, member, invited bool) (*line.GetAllChatMidsResponse, error) {
	f.lists++
	if member || !invited {
		f.t.Fatal("invitation listing requested the wrong chat sets")
	}
	if f.response != nil || f.listErr != nil {
		return f.response, f.listErr
	}
	return &line.GetAllChatMidsResponse{InvitedChatMids: f.invited}, nil
}

func (f *inviteAPI) check(seq int64) {
	if seq == 0 || seq != f.store.state.LastReqSeq {
		f.t.Fatal("invitation change before a persisted sequence")
	}
	f.seqs = append(f.seqs, seq)
}

func (f *inviteAPI) AcceptChatInvitationContext(_ context.Context, seq int64, chat string) error {
	f.check(seq)
	f.accepts = append(f.accepts, chat)
	return f.err
}

func (f *inviteAPI) RejectChatInvitationContext(_ context.Context, seq int64, chat string) error {
	f.check(seq)
	f.rejects = append(f.rejects, chat)
	return f.err
}

func (f *inviteAPI) RegisterE2EEGroupKeyContext(context.Context, int, string, []string, []int, []string) error {
	f.t.Fatal("accepting an invitation registered a group key")
	return nil
}

func (f *inviteAPI) GetLastE2EEGroupSharedKeyContext(context.Context, string) (*line.E2EEGroupSharedKey, error) {
	f.t.Fatal("accepting an invitation fetched a group key")
	return nil, nil
}

func (f *inviteAPI) GetE2EEGroupSharedKeyContext(context.Context, string, int) (*line.E2EEGroupSharedKey, error) {
	f.t.Fatal("accepting an invitation fetched a group key")
	return nil, nil
}

func (f *inviteAPI) SendChatCheckedContext(context.Context, int64, string, string) error {
	f.t.Fatal("accepting an invitation sent a read receipt")
	return nil
}

func inviteSetup(t *testing.T) (*session.Manager, *inviteAPI, *memStore) {
	t.Helper()
	s := &memStore{state: &session.State{Version: 1, MID: "u-self", AccessToken: "synthetic", ExportedKeys: map[string]string{"11": "export"}}}
	f := &inviteAPI{t: t, store: s, invited: []string{otherGroup, invitedGroup, invitedGroup, "not-a-mid"}}
	m := session.NewManager(s)
	m.NewClient = func(string) session.API { return f }
	return m, f, s
}

func TestPendingInvitationsDropsDuplicatesAndMalformedIDs(t *testing.T) {
	m, _, _ := inviteSetup(t)
	pending, err := PendingInvitationsContext(context.Background(), m)
	if err != nil || len(pending) != 2 || pending[0] != otherGroup || pending[1] != invitedGroup {
		t.Fatalf("pending %v %v", pending, err)
	}
	m, f, _ := inviteSetup(t)
	f.response = &line.GetAllChatMidsResponse{}
	if pending, err := PendingInvitationsContext(context.Background(), m); err != nil || len(pending) != 0 {
		t.Fatalf("empty %v %v", pending, err)
	}
}

func TestAcceptAndRejectInvitationOnce(t *testing.T) {
	m, f, s := inviteSetup(t)
	accepted, err := AcceptInvitationContext(context.Background(), m, invitedGroup)
	if err != nil || accepted.Action != "accept_invitation" || accepted.ChatID != invitedGroup || accepted.RequestSequence != s.state.LastReqSeq {
		t.Fatalf("accept %+v %v", accepted, err)
	}
	rejected, err := RejectInvitationContext(context.Background(), m, otherGroup)
	if err != nil || rejected.Action != "reject_invitation" || rejected.RequestSequence != s.state.LastReqSeq || rejected.RequestSequence <= accepted.RequestSequence {
		t.Fatalf("reject %+v %v", rejected, err)
	}
	if len(f.accepts) != 1 || f.accepts[0] != invitedGroup || len(f.rejects) != 1 || f.rejects[0] != otherGroup || f.lists != 2 {
		t.Fatalf("calls %+v", f)
	}
}

func TestInvitationNotPendingIsNeverAttempted(t *testing.T) {
	m, f, s := inviteSetup(t)
	unknown := "c" + strings.Repeat("Z", 43)
	for _, call := range []func(context.Context, *session.Manager, string) (*InvitationResult, error){AcceptInvitationContext, RejectInvitationContext} {
		_, err := call(context.Background(), m, unknown)
		var typed *fault.Error
		e := mutationDetails(t, err)
		if !errors.As(err, &typed) || typed.Code != "invalid_arguments" || e.Mutation.Attempted || e.Mutation.Outcome != fault.NotAttempted || e.Mutation.RequestSequence != nil {
			t.Fatalf("not pending: %v %+v", err, e.Mutation)
		}
	}
	for _, bad := range []string{"", "u" + strings.Repeat("A", 43), "r" + strings.Repeat("A", 43), "Family", "c-short"} {
		if _, err := AcceptInvitationContext(context.Background(), m, bad); err == nil {
			t.Fatalf("accepted invalid ID %q", bad)
		}
	}
	if len(f.accepts)+len(f.rejects) != 0 || s.state.LastReqSeq != 0 {
		t.Fatal("invalid invitation reached LINE or reserved a sequence")
	}
	f.listErr = errors.New("synthetic list failure")
	if _, err := AcceptInvitationContext(context.Background(), m, invitedGroup); err == nil || len(f.accepts) != 0 || s.state.LastReqSeq != 0 {
		t.Fatal("unverified invitation was accepted")
	}
}

func TestInvitationOutcomesAreNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		outcome fault.Outcome
	}{
		{"lost response", io.ErrUnexpectedEOF, fault.Unknown},
		{"rejected", &line.ResponseError{Status: 403}, fault.Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f, s := inviteSetup(t)
			f.err = tc.failure
			_, err := AcceptInvitationContext(context.Background(), m, invitedGroup)
			e := mutationDetails(t, err)
			if len(f.accepts) != 1 || e.Stage != "accept_invitation" || e.Mutation.Outcome != tc.outcome || !e.Mutation.Attempted ||
				e.Mutation.ChatID != invitedGroup || e.Mutation.RequestSequence == nil || *e.Mutation.RequestSequence != s.state.LastReqSeq {
				t.Fatalf("outcome %+v accepts=%d", e, len(f.accepts))
			}
		})
	}
}

func TestInvitationHonorsReadOnlyAndCancellation(t *testing.T) {
	m, f, s := inviteSetup(t)
	m.ReadOnly = true
	if _, err := AcceptInvitationContext(context.Background(), m, invitedGroup); !errors.Is(err, session.ErrReadOnly) || len(f.accepts) != 0 || s.state.LastReqSeq != 0 {
		t.Fatalf("read-only accept: %v", err)
	}
	m.ReadOnly = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RejectInvitationContext(ctx, m, invitedGroup); !errors.Is(err, context.Canceled) || len(f.rejects) != 0 {
		t.Fatalf("cancelled reject: %v", err)
	}
}
