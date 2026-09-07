package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

type testStore struct{ state *session.State }

func (s *testStore) Load() (*session.State, error)   { return s.state, nil }
func (s *testStore) Save(state *session.State) error { s.state = state; return nil }
func (s *testStore) Delete() error                   { s.state = nil; return nil }

type readAPI struct {
	session.API
	pages      []*line.MessageBoxesResponse
	cursors    []string
	ids        []string
	batchSizes []int
}

func (f *readAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u1", DisplayName: "Name\x1b[2J\n注入"}, nil
}
func (f *readAPI) GetMessageBoxes(options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	page := f.pages[len(f.cursors)]
	f.cursors = append(f.cursors, options.MinChatID)
	return page, nil
}
func (f *readAPI) GetAllContactIds() ([]string, error) { return f.ids, nil }
func (f *readAPI) GetContactsV2(ids []string) (*line.ContactsResponse, error) {
	f.batchSizes = append(f.batchSizes, len(ids))
	response := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
	for _, id := range ids {
		response.Contacts[id] = line.ContactWrapper{Contact: line.Contact{DisplayName: id}}
	}
	return response, nil
}

func testApp(api *readAPI) (*App, *bytes.Buffer, *bytes.Buffer) {
	out, diagnostics := new(bytes.Buffer), new(bytes.Buffer)
	m := session.NewManager(&testStore{state: &session.State{AccessToken: "secret"}})
	m.NewClient = func(string) session.API { return api }
	return &App{Out: out, Err: diagnostics, Manager: m, Version: "test", Lock: func() (func(), error) { return func() {}, nil }}, out, diagnostics
}

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
