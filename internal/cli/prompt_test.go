package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type guidedAPI struct {
	chatAPI
	sends, reactions, removals, unsends, logins int
}

func (f *guidedAPI) GetAllContactIds() ([]string, error) { return []string{"u-alice", "u-new"}, nil }
func (f *guidedAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u-self", DisplayName: "Alex"}, nil
}
func (f *guidedAPI) SendMessage(_ int64, m *line.Message) (*line.Message, error) {
	f.sends++
	f.sent = m
	return &line.Message{ID: "999"}, nil
}
func (f *guidedAPI) React(_ int64, _ string, _ line.ReactionType) error { f.reactions++; return nil }
func (f *guidedAPI) CancelReaction(_ int64, _ string) error             { f.removals++; return nil }
func (f *guidedAPI) UnsendMessage(_ int64, _ string) error              { f.unsends++; return nil }
func (f *guidedAPI) DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error) {
	return []byte("file bytes"), nil
}
func (f *guidedAPI) Login(email, password, certificate string) (*line.LoginResult, error) {
	f.logins++
	if email != "you@example.com" || password != "synthetic" {
		return nil, errors.New("unexpected credentials")
	}
	return &line.LoginResult{AuthToken: "synthetic-token", NoE2EE: true}, nil
}

type checkedInput struct {
	t      *testing.T
	locked *bool
	io.Reader
	onRead func()
}

func (r checkedInput) Read(p []byte) (int, error) {
	r.t.Helper()
	if *r.locked {
		r.t.Fatal("prompt held the session lock")
	}
	if r.onRead != nil {
		r.onRead()
	}
	return r.Reader.Read(p)
}

func guidedApp(t *testing.T, input string) (*App, *guidedAPI, *bytes.Buffer, *bytes.Buffer, *bool) {
	t.Helper()
	f := &guidedAPI{chatAPI: chatAPI{names: map[string]string{"u-alice": "Alice", "u-new": "New contact"}, boxes: []line.MessageBox{{ID: "u-alice"}}}}
	f.history = []*line.Message{{ID: "123", From: "u-self", To: "u-alice", Text: "My message"}, {ID: "124", From: "u-alice", To: "u-self", Text: "Their message"}, {ID: "125", From: "u-alice", ContentType: 14, ContentMetadata: map[string]string{"FILE_NAME": "notes.txt"}}}
	a, out, diagnostics := testApp(nil)
	a.Interactive = true
	a.Manager.Store = &testStore{state: &session.State{Version: 1, MID: "u-self", AccessToken: "synthetic-token", Generation: "one", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	locked := false
	a.Lock = func() (func(), error) {
		if locked {
			t.Fatal("nested lock")
		}
		locked = true
		return func() { locked = false }, nil
	}
	a.In = checkedInput{t: t, locked: &locked, Reader: strings.NewReader(input)}
	return a, f, out, diagnostics, &locked
}

func TestGuidedSendSelectsNewContactAndSendsOnce(t *testing.T) {
	a, f, out, diagnostics, _ := guidedApp(t, "/New\n1\n\nHello คุณแฟน\n")
	if err := a.Run([]string{"send"}); err != nil {
		t.Fatal(err)
	}
	if f.sends != 1 || f.sent.To != "u-new" || f.sent.Text != "Hello คุณแฟน" {
		t.Fatal("incorrect recipient, text, or mutation count")
	}
	if !strings.Contains(out.String(), "Sent to New contact") || strings.Contains(out.String(), "u-new") {
		t.Fatal("unfriendly output")
	}
	if !strings.Contains(diagnostics.String(), "Message cannot be empty") {
		t.Fatal("missing empty input feedback")
	}
}

func TestExplicitNameIncludesContactsWithoutChats(t *testing.T) {
	a, f, _, _, _ := guidedApp(t, "unused")
	if err := a.Run([]string{"send", "New contact", "--text", "hello", "--json"}); err != nil {
		t.Fatal(err)
	}
	if f.sends != 1 || f.sent.To != "u-new" {
		t.Fatal("new contact name did not resolve")
	}
	a, f, _, _, _ = guidedApp(t, "unused")
	f.names["u-new"] = "Alice"
	if err := a.Run([]string{"send", "Alice", "--text", "hello"}); err == nil {
		t.Fatal("ambiguous name was accepted")
	}
	if f.sends != 0 {
		t.Fatal("ambiguous recipient received a message")
	}
}

func TestContactHumanAndJSONLimits(t *testing.T) {
	f := &readAPI{}
	for i := 0; i < 25; i++ {
		f.ids = append(f.ids, string(rune('A'+i)))
	}
	a, out, _ := testApp(f)
	if err := a.Run([]string{"contacts"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Showing 20 of 25") {
		t.Fatal("missing default limit")
	}
	out.Reset()
	if err := a.Run([]string{"contacts", "--json"}); err != nil {
		t.Fatal(err)
	}
	var contacts []line.Contact
	if err := json.Unmarshal(out.Bytes(), &contacts); err != nil || len(contacts) != 25 {
		t.Fatal("JSON default was capped", err)
	}
	out.Reset()
	if err := a.Run([]string{"contacts", "--search", "A", "--show-ids"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Showing 1 of 1") {
		t.Fatal("contact search failed")
	}
}

func TestGuidedSendCancellationAndSessionChange(t *testing.T) {
	for _, input := range []string{"q\n", "1\nunfinished text"} {
		a, f, _, _, _ := guidedApp(t, input)
		if err := a.Run([]string{"send"}); !errors.Is(err, ErrCancelled) {
			t.Fatal(err)
		}
		if f.sends != 0 {
			t.Fatal("cancelled send transmitted")
		}
	}
	a, f, _, _, locked := guidedApp(t, "")
	a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader("1\nHello\n"), onRead: func() { a.Manager.Store.(*testStore).state.Generation = "replacement" }}
	if err := a.Run([]string{"send"}); err == nil || !strings.Contains(err.Error(), "account changed") {
		t.Fatal(err)
	}
	if f.sends != 0 {
		t.Fatal("sent from changed session")
	}
}

func TestMissingArgumentsNeverPromptForScripts(t *testing.T) {
	for _, args := range [][]string{{"send", "--json"}, {"send", "--stdin"}, {"messages", "--json"}, {"react", "--json"}, {"download", "--json"}, {"unsend", "--json"}} {
		a, _, _, _, _ := guidedApp(t, "")
		a.Lock = func() (func(), error) { t.Fatal("script missing required arguments opened session"); return nil, nil }
		if err := a.Run(args); err == nil {
			t.Fatal("missing argument accepted")
		}
	}
	a, _, _, _, _ := guidedApp(t, "1\nHello\n")
	a.Interactive = false
	a.Lock = func() (func(), error) { t.Fatal("non-terminal input opened session"); return nil, nil }
	if err := a.Run([]string{"send"}); err == nil {
		t.Fatal("non-terminal bare send accepted")
	}
}

func TestChooserDuplicateNamesPagingAndSearch(t *testing.T) {
	a, f, _, diagnostics, _ := guidedApp(t, "/Alice\n2\nHello\n")
	f.boxes = append(f.boxes, line.MessageBox{ID: "u-other"})
	f.names["u-other"] = "Alice"
	if err := a.Run([]string{"send"}); err != nil {
		t.Fatal(err)
	}
	if f.sent.To != "u-other" || !strings.Contains(diagnostics.String(), "u-alice") || !strings.Contains(diagnostics.String(), "u-other") {
		t.Fatal("duplicate recipients were not distinguished")
	}
	a, _, _, _, _ = guidedApp(t, "n\np\nn\n1\n")
	options := make([]choice, 21)
	for i := range options {
		options[i] = choice{id: string(rune('a' + i)), label: "choice"}
	}
	id, err := a.choose("Choose", options, true)
	if err != nil || id != "u" {
		t.Fatal("page-local selection failed", err)
	}
}

func TestGuidedActionsAndDownload(t *testing.T) {
	for _, tc := range []struct {
		command, input string
		want           int
	}{{"react", "1\n1\n1\n", 1}, {"react", "1\n1\n7\n", 1}, {"unsend", "1\n1\ny\n", 1}, {"unsend", "1\n1\nn\n", 0}} {
		a, f, _, _, _ := guidedApp(t, tc.input)
		err := a.Run([]string{tc.command})
		if tc.want == 0 {
			if !errors.Is(err, ErrCancelled) {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if f.reactions+f.removals+f.unsends != tc.want {
			t.Fatal("unexpected action count")
		}
	}
	path := filepath.Join(t.TempDir(), "saved.txt")
	a, _, _, _, _ := guidedApp(t, "1\n1\n"+path+"\n")
	if err := a.Run([]string{"download"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "file bytes" {
		t.Fatal("download failed", err)
	}
}

func TestGuidedLoginAndKeepExisting(t *testing.T) {
	a, f, _, _, locked := guidedApp(t, "you@example.com\n")
	a.Manager.Store = &testStore{}
	a.Password = func() (string, error) {
		if *locked {
			t.Fatal("password prompt held lock")
		}
		return "synthetic", nil
	}
	if err := a.Run([]string{"login"}); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 {
		t.Fatal("login not performed once")
	}
	a, f, _, _, _ = guidedApp(t, "\n")
	a.Password = func() (string, error) { t.Fatal("keeping session requested password"); return "", nil }
	if err := a.Run([]string{"login"}); err != nil {
		t.Fatal(err)
	}
	if f.logins != 0 {
		t.Fatal("keeping session performed login")
	}
}

func TestReadableHistoryPreservesJSONAndResolvesSenders(t *testing.T) {
	a, f, out, _, _ := guidedApp(t, "1\n")
	f.history = []*line.Message{{ID: "456", From: "u-self", CreatedTime: "2000", Text: "Second\nline"}, {ID: "123", From: "u-alice", CreatedTime: "1000", Text: "First\x1b[2J"}}
	if err := a.Run([]string{"messages"}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Index(text, "First") > strings.Index(text, "Second") || !strings.Contains(text, "Alice") || !strings.Contains(text, "You") || strings.Contains(text, "456") || strings.Contains(text, "\x1b") || !strings.Contains(text, "\n       line") {
		t.Fatal("bad readable history")
	}
	out.Reset()
	if err := a.Run([]string{"messages", "id:u-alice", "--json"}); err != nil {
		t.Fatal(err)
	}
	var history []map[string]any
	if err := json.Unmarshal(out.Bytes(), &history); err != nil || history[0]["id"] != "456" {
		t.Fatal("JSON ordering changed", err)
	}
	if len(history[0]) != 8 {
		t.Fatalf("JSON fields changed: %d", len(history[0]))
	}
}
