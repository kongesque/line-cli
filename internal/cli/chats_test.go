package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type chatAPI struct {
	messageAPI
	boxes     []line.MessageBox
	names     map[string]string
	options   []line.MessageBoxesOptions
	batches   []int
	nameError error
}

func (f *chatAPI) GetAllContactIds() ([]string, error) { return nil, nil }

func (f *chatAPI) GetMessageBoxes(options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	f.options = append(f.options, options)
	return &line.MessageBoxesResponse{MessageBoxes: f.boxes}, nil
}
func (f *chatAPI) GetContactsV2(ids []string) (*line.ContactsResponse, error) {
	if f.nameError != nil {
		return nil, f.nameError
	}
	f.batches = append(f.batches, len(ids))
	result := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
	for _, id := range ids {
		result.Contacts[id] = line.ContactWrapper{Contact: line.Contact{DisplayName: "Original", DisplayNameOverridden: f.names[id]}}
	}
	return result, nil
}
func (f *chatAPI) GetChats(ids []string, members, invitees bool) (*line.GetChatsResponse, error) {
	if len(ids) > 20 {
		return nil, errors.New("group lookup batch exceeds the verified API limit")
	}
	if members || invitees {
		return nil, errors.New("unnecessary membership lookup")
	}
	if f.nameError != nil {
		return nil, f.nameError
	}
	f.batches = append(f.batches, len(ids))
	result := &line.GetChatsResponse{}
	for _, id := range ids {
		result.Chats = append(result.Chats, line.Chat{ChatMid: id, ChatName: f.names[id]})
	}
	return result, nil
}

func TestChatsJSONCompatibilityAndEmptySearch(t *testing.T) {
	f := &chatAPI{boxes: []line.MessageBox{{ID: "u-one", UnreadCount: "2"}}, names: map[string]string{"u-one": "Alice"}}
	a := chatTestApp(f)
	if err := a.Run([]string{"chats", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	if err := json.Unmarshal([]byte(a.Out.(fmt.Stringer).String()), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || len(result[0]) != 3 || result[0]["id"] != "u-one" || result[0]["unread_count"] != float64(2) || len(f.batches) != 0 || f.options[0].ActiveOnly {
		t.Fatal("original JSON contract changed")
	}
	a = chatTestApp(f)
	if err := a.Run([]string{"chats", "--search", "nobody", "--json"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(a.Out.(fmt.Stringer).String()) != "[]" {
		t.Fatal("empty search must be a JSON array")
	}
}

func chatTestApp(f *chatAPI) *App {
	a, _, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return f }
	return a
}

func TestChatsDefaultNamesRecencyAndLimit(t *testing.T) {
	f := &chatAPI{names: make(map[string]string)}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("c%032x", i)
		f.boxes = append(f.boxes, line.MessageBox{ID: id, LastMessages: []line.Message{{CreatedTime: json.Number(fmt.Sprint(1700000000000 + i*1000))}}})
		f.names[id] = fmt.Sprintf("家人 %02d", i)
	}
	a := chatTestApp(f)
	if err := a.Run([]string{"chats"}); err != nil {
		t.Fatal(err)
	}
	output := a.Out.(fmt.Stringer).String()
	if !strings.Contains(output, "Showing 20 of 25") || !strings.Contains(output, "家人 24") || strings.Contains(output, "家人 04") || strings.Contains(output, f.boxes[24].ID) {
		t.Fatalf("unexpected output: %s", output)
	}
	if strings.Index(output, "家人 24") > strings.Index(output, "家人 23") {
		t.Fatal("not newest first")
	}
	if !f.options[0].ActiveOnly || f.options[0].LastMessagesPerMessageBoxCount != 1 || fmt.Sprint(f.batches) != "[20]" {
		t.Fatal("wrong scope or enrichment limit")
	}
}

func TestChatSearchIncludesInactiveAndSearchesBeforeLimiting(t *testing.T) {
	f := &chatAPI{names: map[string]string{"u-one": "Alice", "c-two": "家人", "c-three": "ALICE Club"}, boxes: []line.MessageBox{{ID: "u-one"}, {ID: "c-two"}, {ID: "c-three"}}}
	a := chatTestApp(f)
	if err := a.Run([]string{"chats", "--search", "alice", "--limit", "1", "--show-ids"}); err != nil {
		t.Fatal(err)
	}
	output := a.Out.(fmt.Stringer).String()
	if !strings.Contains(output, "Showing 1 of 2") || !strings.Contains(output, "Alice") || !strings.Contains(output, "u-one") || f.options[0].ActiveOnly {
		t.Fatalf("bad search: %s", output)
	}
}

func TestNameResolutionExactUniqueAndNoSendOnAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		selector string
		names    map[string]string
		want     string
	}{
		{"charlie", map[string]string{"u-one": "Charlie", "c-two": "Charles"}, "u-one"},
		{"家人", map[string]string{"u-one": "Alice", "c-two": "家人"}, "c-two"},
		{"Ali", map[string]string{"u-one": "Alice", "c-two": "Alison"}, ""},
		{"Alice", map[string]string{"u-one": "Alice", "c-two": "ALICE"}, ""},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			f := &chatAPI{names: tc.names, boxes: []line.MessageBox{{ID: "u-one"}, {ID: "c-two"}}}
			a := chatTestApp(f)
			a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "secret", NoE2EE: true}}
			got, err := a.resolveChat(tc.selector)
			if (err != nil) != (tc.want == "") || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
			if tc.want == "" {
				if err := a.Run([]string{"send", tc.selector, "--text", "test"}); err == nil || f.sent != nil {
					t.Fatal("unresolved name sent")
				}
			} else if tc.want == "u-one" {
				if err := a.Run([]string{"send", tc.selector, "--text", "test"}); err != nil {
					t.Fatal(err)
				}
				if f.sent == nil || f.sent.To != tc.want {
					t.Fatal("wrong send destination")
				}
			}
		})
	}
}

func TestDirectIDsDoNotLookupNames(t *testing.T) {
	a := chatTestApp(nil)
	for _, id := range []string{"U" + strings.Repeat("a", 43), "c" + strings.Repeat("0", 32), "id:u-peer"} {
		got, err := a.resolveChat(id)
		if err != nil || got != strings.TrimPrefix(id, "id:") {
			t.Fatal("ID lookup failed", err)
		}
	}
}

func TestNameLookupBatchesAndFailureStopsSend(t *testing.T) {
	f := &chatAPI{names: make(map[string]string)}
	chats := make([]Chat, 0)
	for i := 0; i < 101; i++ {
		chats = append(chats, Chat{ID: fmt.Sprintf("u%03d", i)})
	}
	for i := 0; i < 51; i++ {
		chats = append(chats, Chat{ID: fmt.Sprintf("c%03d", i)})
	}
	a := chatTestApp(f)
	if err := a.nameChats(chats); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.batches) != "[100 1 20 20 11]" {
		t.Fatal(f.batches)
	}
	f.boxes = []line.MessageBox{{ID: "u-one"}}
	f.nameError = errors.New("lookup failed")
	if err := a.Run([]string{"send", "Alice", "--text", "test"}); err == nil || f.sent != nil {
		t.Fatal("lookup failure did not stop send")
	}
}

func TestChatsSanitizesNamesAndShowsUnknownIDs(t *testing.T) {
	f := &chatAPI{names: map[string]string{"c-one": "Name\x1b[2J\n注入"}, boxes: []line.MessageBox{{ID: "c-one"}, {ID: "c-two"}}}
	a := chatTestApp(f)
	if err := a.Run([]string{"chats"}); err != nil {
		t.Fatal(err)
	}
	output := a.Out.(fmt.Stringer).String()
	if strings.Contains(output, "\x1b") || strings.Contains(output, "\n注入") || !strings.Contains(output, "c-two") {
		t.Fatal("unsafe name or inaccessible unnamed chat")
	}
}

func TestChatOptionsValidateBeforeSession(t *testing.T) {
	for _, args := range [][]string{{"chats", "--help"}, {"chats", "--limit", "-1"}, {"chats", "extra"}} {
		a := chatTestApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened session"); return nil, nil }
		_ = a.Run(args)
	}
}
