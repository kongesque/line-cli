package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestHelpAndArgumentErrorsNeverOpenSession(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"version"}, {"login", "--help"}, {"whoami", "--help"}, {"invalid"}, {"login"}, {"chats", "extra"}, {"logout", "--json"}, {"whoami", "--json", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, _, _ := testApp(nil)
			a.Lock = func() (func(), error) { t.Fatal("opened session before validating arguments"); return nil, nil }
			_ = a.Run(args)
		})
	}
}

func TestWhoamiJSONKeepsStdoutMachineReadable(t *testing.T) {
	a, out, diagnostics := testApp(&readAPI{})
	if err := a.Run([]string{"whoami", "--json"}); err != nil {
		t.Fatal(err)
	}
	var profile line.Profile
	if err := json.Unmarshal(out.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Mid != "u1" || diagnostics.Len() != 0 || strings.Contains(out.String(), "secret") {
		t.Fatal("invalid output")
	}
}

func TestHumanOutputCannotInjectTerminalControls(t *testing.T) {
	a, out, _ := testApp(&readAPI{})
	if err := a.Run([]string{"whoami"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "\n") != 2 {
		t.Fatal("untrusted name injected terminal controls")
	}
}

func TestChatsPaginatesAndDeduplicates(t *testing.T) {
	f := &readAPI{pages: []*line.MessageBoxesResponse{
		{MessageBoxes: []line.MessageBox{{ID: "a", UnreadCount: "1"}, {ID: "b"}}, HasNext: true},
		{MessageBoxes: []line.MessageBox{{ID: "b"}, {ID: "c"}}},
	}}
	a, out, _ := testApp(f)
	if err := a.Run([]string{"chats", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result []Chat
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 || len(f.cursors) != 2 || f.cursors[1] != "b" {
		t.Fatal("incorrect pagination")
	}
}

func TestChatsRejectsStuckCursorWithoutPartialOutput(t *testing.T) {
	f := &readAPI{pages: []*line.MessageBoxesResponse{
		{MessageBoxes: []line.MessageBox{{ID: "a"}}, HasNext: true},
		{MessageBoxes: []line.MessageBox{{ID: "a"}}, HasNext: true},
	}}
	a, out, _ := testApp(f)
	if err := a.Run([]string{"chats", "--json"}); err == nil {
		t.Fatal("stuck cursor accepted")
	}
	if out.Len() != 0 {
		t.Fatal("partial output presented as success")
	}
}

func TestContactsBatchesAndPreservesMapIDs(t *testing.T) {
	f := &readAPI{}
	for i := 200; i >= 0; i-- {
		f.ids = append(f.ids, fmt.Sprintf("u%03d", i))
	}
	a, out, _ := testApp(f)
	if err := a.Run([]string{"contacts", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result []line.Contact
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 201 || result[0].Mid != "u000" || fmt.Sprint(f.batchSizes) != "[100 100 1]" {
		t.Fatal("incorrect batching or ordering")
	}
}

func TestEmptyListsAreJSONArrays(t *testing.T) {
	a, out, _ := testApp(&readAPI{})
	if err := a.Run([]string{"contacts", "--json"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatal("empty list should be []")
	}
}

type rejectedSessionAPI struct{ readAPI }

func (*rejectedSessionAPI) GetProfile() (*line.Profile, error) {
	return nil, fmt.Errorf(`API error 401: {"code":10004,"message":"REQUEST_NEED_LOGIN","private":"synthetic-secret"}`)
}

func TestWhoamiRejectedSessionHasSafeReloginGuidance(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		a, out, _ := testApp(nil)
		a.Manager.NewClient = func(string) session.API { return &rejectedSessionAPI{} }
		args := []string{"whoami"}
		if jsonOutput {
			args = append(args, "--json")
		}
		err := a.Run(args)
		if err == nil || !strings.Contains(err.Error(), "session was invalidated") || strings.Contains(err.Error(), "168 hours") || !strings.Contains(err.Error(), "line login") || strings.Contains(err.Error(), "synthetic-secret") || out.Len() != 0 {
			t.Fatal("missing safe re-login guidance or contaminated stdout", err)
		}
	}
}

func (f *rejectedSessionAPI) GetProfileContext(_ context.Context) (*line.Profile, error) {
	return f.GetProfile()
}

func TestLogoutRevokesRemotelyAndSupportsLocalOnly(t *testing.T) {
	t.Run("revokes remotely and deletes local session", func(t *testing.T) {
		f := &readAPI{}
		a, out, _ := testApp(f)
		store := a.Manager.Store.(*testStore)
		if err := a.Run([]string{"logout"}); err != nil {
			t.Fatal(err)
		}
		if f.logoutCalls != 1 || store.state != nil || !strings.Contains(out.String(), "revoked") {
			t.Fatalf("logout did not revoke/delete: calls=%d state=%v out=%q", f.logoutCalls, store.state, out.String())
		}
	})

	t.Run("already logged out remotely still deletes local session", func(t *testing.T) {
		f := &readAPI{logoutErr: fmt.Errorf(`API error 401: {"code":10004,"message":"REQUEST_NEED_LOGIN"}`)}
		a, _, _ := testApp(f)
		store := a.Manager.Store.(*testStore)
		if err := a.Run([]string{"logout"}); err != nil {
			t.Fatal(err)
		}
		if f.logoutCalls != 1 || store.state != nil {
			t.Fatalf("already-logged-out session was not cleaned up: calls=%d state=%v", f.logoutCalls, store.state)
		}
	})

	t.Run("network failure preserves local session and instructs --local-only", func(t *testing.T) {
		f := &readAPI{logoutErr: fmt.Errorf("connection refused")}
		a, _, _ := testApp(f)
		store := a.Manager.Store.(*testStore)
		err := a.Run([]string{"logout"})
		if err == nil || !strings.Contains(err.Error(), "--local-only") || store.state == nil || f.logoutCalls != 1 {
			t.Fatalf("logout failure did not preserve state or instruct --local-only: err=%v state=%v calls=%d", err, store.state, f.logoutCalls)
		}
	})

	t.Run("--local-only skips remote call and deletes local session", func(t *testing.T) {
		f := &readAPI{logoutErr: fmt.Errorf("offline")}
		a, out, _ := testApp(f)
		store := a.Manager.Store.(*testStore)
		if err := a.Run([]string{"logout", "--local-only"}); err != nil {
			t.Fatal(err)
		}
		if f.logoutCalls != 0 || store.state != nil || !strings.Contains(out.String(), "without contacting LINE") {
			t.Fatalf("--local-only contacted LINE or kept state: calls=%d state=%v out=%q", f.logoutCalls, store.state, out.String())
		}
	})
}
