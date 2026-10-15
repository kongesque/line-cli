package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type pagedCLIAPI struct {
	messageAPI
	older   []*line.Message
	anchors []line.MessageIDWrapper
}

func (f *pagedCLIAPI) GetPreviousMessagesV2Context(_ context.Context, _ string, anchor line.MessageIDWrapper, _ int) ([]*line.Message, error) {
	f.anchors = append(f.anchors, anchor)
	return f.older, nil
}
func cliCursor(chat string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"chat_id":"` + chat + `","message_id":"9007199254740993","delivered_time":"100"}`))
}
func TestHistoryPageFlagsValidateBeforeSession(t *testing.T) {
	for _, args := range [][]string{
		{"messages", "u-peer", "--page"},
		{"messages", "u-peer", "--page", "--json", "--cursor", ""},
		{"messages", "u-peer", "--cursor", "bad", "--json"},
		{"messages", "u-peer", "--page", "--json", "--cursor", "bad"},
		{"messages", "id:u-peer", "--page", "--json", "--cursor", cliCursor("u-other")},
		{"messages", "u-peer", "--page", "--json", "--limit", "101"},
	} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("invalid options accessed session"); return nil, nil }
		if err := a.Run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestHistoryPageCLIAndLegacyOutput(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &pagedCLIAPI{}
	f.history = []*line.Message{{ID: "9007199254740993", From: "u-peer", To: "u-self", CreatedTime: "2", DeliveredTime: "100", Text: "safe"}}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"messages", "id:u-peer", "--page", "--json"}); err != nil {
		t.Fatal(err)
	}
	var page messaging.HistoryPage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.SchemaVersion != 1 || page.ChatID != "u-peer" || page.NextCursor == "" || page.Exhausted || len(page.Messages) != 1 {
		t.Fatalf("page: %+v", page)
	}
	out.Reset()
	if err := a.Run([]string{"messages", "id:u-peer", "--page", "--json", "--cursor", page.NextCursor}); err != nil {
		t.Fatal(err)
	}
	if len(f.anchors) != 1 || f.anchors[0].MessageID != "9007199254740993" || f.anchors[0].DeliveredTime != "100" || !strings.Contains(out.String(), `"exhausted": true`) {
		t.Fatalf("continuation: %s", out.String())
	}
	out.Reset()
	if err := a.Run([]string{"messages", "id:u-peer", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "[") || strings.Contains(out.String(), "cursor") {
		t.Fatal("changed legacy array")
	}
}
func TestHistoryPagePartialDecryptionFailureKeepsEnvelope(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &pagedCLIAPI{}
	f.history = []*line.Message{{ID: "123", From: "u-peer", To: "u-self", DeliveredTime: "100", ContentMetadata: map[string]string{"e2eeVersion": "2"}}}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	err := a.Run([]string{"--error-format=json", "messages", "id:u-peer", "--page", "--json"})
	if err == nil {
		t.Fatal("expected partial failure")
	}
	var page messaging.HistoryPage
	if json.Unmarshal(out.Bytes(), &page) != nil || len(page.Messages) != 1 || page.Messages[0].Status != "decryption_failed" || page.NextCursor == "" {
		t.Fatalf("partial output: %s", out.String())
	}
	if DescribeError(err, "decode", nil).Code != "decryption_failed" {
		t.Fatal("lost error code")
	}
}

func TestHistoryCursorWithNameStartingLikeMID(t *testing.T) {
	a, f, out, _, _ := guidedApp(t, "")
	f.names["u-alice"] = "Carol"
	f.history = []*line.Message{{ID: "9007199254740993", From: "u-alice", To: "u-self", DeliveredTime: "100"}}
	// A valid cursor with a name selector must reach name resolution, not reject
	// the human name as though it were an explicit MID.
	if err := a.Run([]string{"messages", "Carol", "--page", "--json", "--cursor", cliCursor("u-other")}); err == nil || !strings.Contains(err.Error(), "different chat") {
		t.Fatalf("cursor name: %v", err)
	}
	if len(f.batches) == 0 || out.Len() != 0 {
		t.Fatal("name was not resolved before chat binding")
	}
}
