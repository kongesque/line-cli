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
