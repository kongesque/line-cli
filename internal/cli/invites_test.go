package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/pkg/line"
)

var (
	groupFamily = "c" + strings.Repeat("F", 43)
	groupWork   = "c" + strings.Repeat("W", 43)
)

func (f *guidedAPI) GetAllChatMidsContext(_ context.Context, member, invited bool) (*line.GetAllChatMidsResponse, error) {
	if member || !invited {
		panic("invitation listing requested joined chats")
	}
	return &line.GetAllChatMidsResponse{InvitedChatMids: f.invited}, f.listErr
}

func (f *guidedAPI) AcceptChatInvitationContext(_ context.Context, seq int64, chat string) error {
	f.inviteSeqs = append(f.inviteSeqs, seq)
	f.accepts = append(f.accepts, chat)
	return f.inviteErr
}

func (f *guidedAPI) RejectChatInvitationContext(_ context.Context, seq int64, chat string) error {
	f.inviteSeqs = append(f.inviteSeqs, seq)
	f.rejects = append(f.rejects, chat)
	return f.inviteErr
}

func (f *guidedAPI) RegisterE2EEGroupKeyContext(context.Context, int, string, []string, []int, []string) error {
	panic("accepting an invitation registered a group key")
}

func (f *guidedAPI) GetLastE2EEGroupSharedKeyContext(context.Context, string) (*line.E2EEGroupSharedKey, error) {
	panic("accepting an invitation fetched a group key")
}

func inviteApp(t *testing.T, input string) (*App, *guidedAPI, *strings.Builder, *strings.Builder) {
	t.Helper()
	a, f, out, diagnostics, _ := guidedApp(t, input)
	f.invited = []string{groupFamily, groupWork}
	f.names[groupFamily] = "Family"
	o, d := new(strings.Builder), new(strings.Builder)
	a.Out, a.Err = io.MultiWriter(out, o), io.MultiWriter(diagnostics, d)
	return a, f, o, d
}

func TestInvitesValidationNeverOpensSession(t *testing.T) {
	for _, args := range [][]string{
		{"invites", "--help"}, {"invites", "accept", "--help"}, {"invites", "extra"}, {"invites", "accept"},
		{"invites", "accept", "Family"}, {"invites", "accept", "u" + strings.Repeat("A", 43)}, {"invites", "reject", "r" + strings.Repeat("A", 43)},
		{"invites", "accept", groupFamily, "extra"}, {"invites", "--unknown"}, {"invites", "reject", "--json"},
	} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened session for invalid arguments"); return nil, nil }
		_ = a.Run(args)
	}
}

func TestInvitesListHumanAndJSON(t *testing.T) {
	a, f, out, diagnostics := inviteApp(t, "")
	a.Interactive = false
	if err := a.Run([]string{"invites"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Pending group invitations (2)") || !strings.Contains(got, "Family") || !strings.Contains(got, groupWork) || !strings.Contains(got, "(name unavailable)") {
		t.Fatalf("human list %q", got)
	}
	if !strings.Contains(diagnostics.String(), "Some group names are unavailable") || len(f.accepts)+len(f.rejects) != 0 {
		t.Fatal("missing name note or unexpected change")
	}
	out.Reset()
	if err := a.Run([]string{"invites", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schema_version"`
		Invitations   []struct {
			ChatID     string  `json:"chat_id"`
			Name       *string `json:"name"`
			NameStatus string  `json:"name_status"`
		} `json:"invitations"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || len(result.Invitations) != 2 || result.Invitations[0].ChatID != groupFamily || *result.Invitations[0].Name != "Family" ||
		result.Invitations[1].Name != nil || result.Invitations[1].NameStatus != "unavailable" {
		t.Fatalf("JSON %+v", result)
	}
	a, f, out, _ = inviteApp(t, "")
	f.invited = nil
	if err := a.Run([]string{"invites", "--json"}); err != nil || strings.TrimSpace(out.String()) != "{\n  \"schema_version\": 1,\n  \"invitations\": []\n}" {
		t.Fatalf("empty JSON %q %v", out.String(), err)
	}
	a, f, out, _ = inviteApp(t, "")
	f.nameError = errors.New("synthetic name failure")
	if err := a.Run([]string{"invites", "--json"}); err != nil || !strings.Contains(out.String(), `"name_status": "unavailable"`) {
		t.Fatalf("names unavailable must not hide invitations: %v %q", err, out.String())
	}
}

func TestInvitesAcceptRejectExplicit(t *testing.T) {
	a, f, out, _ := inviteApp(t, "")
	if err := a.Run([]string{"invites", "accept", groupFamily}); err != nil || out.String() != "Joined the group.\n" {
		t.Fatalf("accept %q %v", out.String(), err)
	}
	out.Reset()
	if err := a.Run([]string{"invites", "reject", groupWork, "--json"}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 || result["action"] != "reject_invitation" || result["chat_id"] != groupWork || result["request_sequence"] != float64(f.inviteSeqs[1]) {
		t.Fatalf("JSON %v", result)
	}
	if len(f.accepts) != 1 || f.accepts[0] != groupFamily || len(f.rejects) != 1 || f.rejects[0] != groupWork || f.sends != 0 || f.reads != 0 {
		t.Fatalf("calls %+v", f)
	}
}

func TestInvitesNotPendingAndOutcomes(t *testing.T) {
	a, f, _, _ := inviteApp(t, "")
	unknown := "c" + strings.Repeat("Z", 43)
	err := a.Run([]string{"--error-format=json", "invites", "accept", unknown})
	var machine *MachineError
	if !errors.As(err, &machine) || machine.Detail.Code != "invalid_arguments" || len(f.accepts) != 0 {
		t.Fatalf("not pending: %v", err)
	}
	if op := machine.Detail.Mutation; op == nil || op.Action != "accept_invitation" || op.ChatID != unknown || op.Attempted || op.RequestSequence != nil {
		t.Fatalf("mutation %+v", op)
	}
	for _, tc := range []struct {
		err     error
		code    string
		outcome fault.Outcome
	}{
		{io.ErrUnexpectedEOF, "remote_failed", fault.Unknown},
		{&line.ResponseError{Status: 403}, "authentication_failed", fault.Rejected},
	} {
		a, f, out, _ := inviteApp(t, "")
		f.inviteErr = tc.err
		err := a.Run([]string{"--error-format=json", "invites", "reject", groupFamily, "--json"})
		if !errors.As(err, &machine) || machine.Detail.Code != tc.code || out.Len() != 0 || len(f.rejects) != 1 {
			t.Fatalf("outcome %v", err)
		}
		if op := machine.Detail.Mutation; op.Action != "reject_invitation" || op.Outcome != tc.outcome || !op.Attempted || op.RequestSequence == nil || *op.RequestSequence != f.inviteSeqs[0] {
			t.Fatalf("mutation %+v", op)
		}
	}
}

func TestInvitesGuidedChooseAcceptRejectSkip(t *testing.T) {
	a, f, out, diagnostics := inviteApp(t, "1\n1\n")
	if err := a.Run([]string{"invites"}); err != nil {
		t.Fatal(err)
	}
	if len(f.accepts) != 1 || f.accepts[0] != groupFamily || !strings.Contains(out.String(), "Joined Family.") || !strings.Contains(diagnostics.String(), "Accept, reject, or skip") {
		t.Fatalf("guided accept %+v %q", f, out.String())
	}
	a, f, out, _ = inviteApp(t, "2\n2\n")
	if err := a.Run([]string{"invites"}); err != nil || len(f.rejects) != 1 || f.rejects[0] != groupWork || !strings.Contains(out.String(), "Rejected the invitation to the group.") {
		t.Fatalf("guided reject %v %+v %q", err, f, out.String())
	}
	a, f, out, _ = inviteApp(t, "1\n3\n")
	if err := a.Run([]string{"invites"}); err != nil || len(f.accepts)+len(f.rejects) != 0 || !strings.Contains(out.String(), "Skipped. Nothing was changed.") {
		t.Fatalf("guided skip %v %q", err, out.String())
	}
	a, f, out, _ = inviteApp(t, "2\n")
	if err := a.Run([]string{"invites", "accept"}); err != nil || len(f.accepts) != 1 || f.accepts[0] != groupWork || out.String() != "Joined the group.\n" {
		t.Fatalf("guided accept command %v %+v %q", err, f, out.String())
	}
	a, f, _, diagnostics = inviteApp(t, "")
	if err := a.Run([]string{"--read-only", "invites"}); err != nil || len(f.accepts)+len(f.rejects) != 0 || !strings.Contains(diagnostics.String(), "Read-only mode is on") {
		t.Fatalf("read-only guided list %v", err)
	}
	a, f, out, _ = inviteApp(t, "")
	f.invited = nil
	if err := a.Run([]string{"invites"}); err != nil || !strings.Contains(out.String(), "No pending group invitations.") {
		t.Fatalf("empty guided list %v %q", err, out.String())
	}
}
