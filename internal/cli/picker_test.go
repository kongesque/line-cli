package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// pickerApp adds a second "Alice" and a group whose name contains "ali".
func pickerApp(t *testing.T, input string) (*App, *guidedAPI) {
	t.Helper()
	a, f, _, _, _ := guidedApp(t, input)
	f.names["u-alice2"] = "Alice"
	f.names["c-bali"] = "Bali trip"
	f.boxes = []line.MessageBox{{ID: "u-alice"}, {ID: "u-alice2"}, {ID: "c-bali"}}
	return a, f
}

func TestPickerResolvesUnmatchedNameByExplicitChoice(t *testing.T) {
	a, f := pickerApp(t, "3\n")
	out, diagnostics := a.Out.(fmt.Stringer), a.Err.(fmt.Stringer)
	if err := a.Run([]string{"send", "ali", "--text", "hello"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), `No chat or contact is named "ali". Choose one`) || !strings.Contains(diagnostics.String(), "Bali trip · Group · c-bali") {
		t.Fatalf("picker %q", diagnostics.String())
	}
	if f.sends != 1 || f.sent.To != "c-bali" || !strings.Contains(out.String(), "Sent to Bali trip") {
		t.Fatalf("sent to %+v %q", f.sent, out.String())
	}
}

func TestPickerDisambiguatesExactDuplicates(t *testing.T) {
	a, f := pickerApp(t, "2\n")
	diagnostics := a.Err.(fmt.Stringer)
	if err := a.Run([]string{"send", "ALICE", "--text", "hello"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), `2 chats or contacts are named "ALICE"`) || f.sent.To != "u-alice2" {
		t.Fatalf("picked %+v from %q", f.sent, diagnostics.String())
	}
	a, f = pickerApp(t, "q\n")
	if err := a.Run([]string{"send", "Alice", "--text", "hello"}); !errors.Is(err, ErrCancelled) || f.sends != 0 {
		t.Fatalf("cancelled picker sent: %v", err)
	}
}

func TestPickerWorksForOtherCommands(t *testing.T) {
	a, f := pickerApp(t, "1\n")
	if err := a.Run([]string{"read", "alic"}); err != nil || f.reads != 1 || f.readChat != "u-alice" {
		t.Fatalf("read via picker: %v %+v", err, f)
	}
	a, f = pickerApp(t, "1\n")
	if err := a.Run([]string{"react", "alic", "--message", "124", "--reaction", "like"}); err != nil || f.reactions != 1 {
		t.Fatalf("react via picker: %v", err)
	}
}

func TestNonInteractiveNamesFailWithCandidates(t *testing.T) {
	for _, args := range [][]string{
		{"send", "ali", "--text", "hello"},
		{"send", "Alice", "--text", "hello", "--json"},
		{"read", "Bali", "--json"},
		{"messages", "ali", "--json"},
	} {
		a, f := pickerApp(t, "")
		a.Interactive = strings.Contains(strings.Join(args, " "), "--json")
		err := a.Run(args)
		if err == nil || f.sends+f.reads != 0 {
			t.Fatalf("%q resolved a fuzzy name: %v", args, err)
		}
		if !strings.Contains(err.Error(), "u-alice") && !strings.Contains(err.Error(), "c-bali") || !strings.Contains(err.Error(), "Use a full ID") {
			t.Fatalf("%q: no candidates in %v", args, err)
		}
		if strings.Contains(a.Err.(fmt.Stringer).String(), "Choose") {
			t.Fatalf("%q prompted without a terminal", args)
		}
	}
	// A single similar candidate is still never chosen for a change.
	a, f := pickerApp(t, "")
	a.Interactive = false
	if err := a.Run([]string{"send", "bali", "--text", "hello"}); err == nil || f.sends != 0 || !strings.Contains(err.Error(), "Bali trip (Group c-bali)") {
		t.Fatalf("single candidate: %v", err)
	}
	// JSON errors carry the candidate list.
	a, _ = pickerApp(t, "")
	var machine *MachineError
	if err := a.Run([]string{"--error-format=json", "send", "ali", "--text", "hi"}); !errors.As(err, &machine) || machine.Detail.Code != "operation_failed" || !strings.Contains(machine.Detail.Message, "c-bali") {
		t.Fatalf("JSON candidates %+v", err)
	}
}

func TestCandidateListIsBounded(t *testing.T) {
	a, f := pickerApp(t, "")
	a.Interactive = false
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("c-team%d", i)
		f.names[id] = fmt.Sprintf("Team %d", i)
		f.boxes = append(f.boxes, line.MessageBox{ID: id})
	}
	err := a.Run([]string{"send", "team", "--text", "hello"})
	if err == nil || !strings.Contains(err.Error(), "and 2 more") || strings.Count(err.Error(), "(Group ") != 5 {
		t.Fatalf("bounded list: %v", err)
	}
	a, _ = pickerApp(t, "")
	a.Interactive = false
	if err := a.Run([]string{"send", "nobody-like-this", "--text", "hello"}); err == nil || !strings.Contains(err.Error(), "line chats --search NAME") {
		t.Fatalf("no candidates: %v", err)
	}
}

func TestPickerRechecksAccountAfterChoosing(t *testing.T) {
	a, f, _, _, _ := guidedApp(t, "")
	f.names["c-bali"] = "Bali trip"
	f.boxes = []line.MessageBox{{ID: "c-bali"}}
	store := a.Manager.Store.(*testStore)
	a.In = checkedInput{t: t, locked: new(bool), Reader: strings.NewReader("1\n"), onRead: func() {
		store.state = &session.State{Version: 1, MID: "u-other", AccessToken: "other", Generation: "two", NoE2EE: true}
	}}
	if err := a.Run([]string{"send", "bali", "--text", "hello"}); err == nil || !strings.Contains(err.Error(), "account changed") || f.sends != 0 {
		t.Fatalf("account change: %v", err)
	}
}
