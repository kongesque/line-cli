package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
)

func unreadFixture() *chatAPI {
	return &chatAPI{boxes: []line.MessageBox{
		{ID: "u-one", UnreadCount: "2", LastMessages: []line.Message{{CreatedTime: "300"}}},
		{ID: "u-unknown"},
		{ID: "u-zero", UnreadCount: "0", LastMessages: []line.Message{{CreatedTime: "400"}}},
		{ID: "c-group", UnreadCount: "15", LastMessages: []line.Message{{CreatedTime: "200"}}},
		{ID: "u-bad", UnreadCount: "abc"},
	}, names: map[string]string{"u-one": "Alice", "u-zero": "Zed", "c-group": "Family", "u-unknown": "Unknown", "u-bad": "Bad"}}
}

func TestChatsUnreadLegacyJSONKeepsShape(t *testing.T) {
	f := unreadFixture()
	a := chatTestApp(f)
	if err := a.Run([]string{"chats", "--unread", "--json"}); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["id"] != "u-one" || rows[1]["id"] != "c-group" || len(rows[0]) != 3 || f.options[0].ActiveOnly || len(f.batches) != 0 {
		t.Fatalf("legacy unread JSON %v", rows)
	}
	a = chatTestApp(unreadFixture())
	if err := a.Run([]string{"chats", "--unread", "--json", "--limit", "1"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &rows); err != nil || len(rows) != 1 || rows[0]["id"] != "u-one" {
		t.Fatalf("limit after filter: %v %v", rows, err)
	}
}

func TestChatsUnreadDetailsAndSearch(t *testing.T) {
	a := chatTestApp(unreadFixture())
	if err := a.Run([]string{"chats", "--unread", "--details", "--json"}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Chats []struct {
			ID          string       `json:"id"`
			UnreadCount *json.Number `json:"unread_count"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Chats) != 2 || envelope.Chats[0].ID != "u-one" || envelope.Chats[1].ID != "c-group" || envelope.Chats[1].UnreadCount == nil || *envelope.Chats[1].UnreadCount != "15" {
		t.Fatalf("details %+v", envelope)
	}
	a = chatTestApp(unreadFixture())
	if err := a.Run([]string{"chats", "--unread", "--search", "fam", "--json"}); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &rows); err != nil || len(rows) != 1 || rows[0]["name"] != "Family" {
		t.Fatalf("search %v %v", rows, err)
	}
}

func TestChatsUnreadHuman(t *testing.T) {
	f := unreadFixture()
	a := chatTestApp(f)
	if err := a.Run([]string{"chats", "--unread", "--show-ids", "--all"}); err != nil {
		t.Fatal(err)
	}
	out := a.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "Family") || strings.Contains(out, "Zed") || strings.Contains(out, "Unknown") ||
		strings.Contains(out, "Bad") || !strings.Contains(out, "c-group") || !strings.Contains(out, "Showing 2 of 2 unread conversations") || f.options[0].ActiveOnly {
		t.Fatalf("human unread %q", out)
	}
	if strings.Index(out, "Alice") > strings.Index(out, "Family") {
		t.Fatal("unread rows lost activity order")
	}
	a = chatTestApp(&chatAPI{boxes: []line.MessageBox{{ID: "u-zero", UnreadCount: "0"}}, names: map[string]string{"u-zero": "Zed"}})
	if err := a.Run([]string{"chats", "--unread"}); err != nil || !strings.Contains(a.Out.(*bytes.Buffer).String(), "No unread conversations found") {
		t.Fatalf("empty unread: %v %q", err, a.Out.(*bytes.Buffer).String())
	}
}
