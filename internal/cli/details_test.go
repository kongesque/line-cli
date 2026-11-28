package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestDetailsFlagsValidateBeforeStorage(t *testing.T) {
	for _, args := range [][]string{{"messages", "id:u-peer", "--details"}, {"messages", "id:u-peer", "--json", "--resolve-names"}, {"chats", "--details"}, {"send", "id:u-peer", "--details", "--json"}} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("invalid flags opened storage"); return nil, nil }
		if a.Run(args) == nil {
			t.Fatal("invalid flags accepted")
		}
	}
}
func TestDetailedHistoryAndPage(t *testing.T) {
	for _, page := range []bool{false, true} {
		a, out, _ := testApp(nil)
		f := &chatAPI{names: map[string]string{"u-peer": "Peer"}}
		f.history = []*line.Message{{ID: "9007199254740993", From: "u-peer", To: "u-self", CreatedTime: "10", DeliveredTime: "20", ContentType: 14, ContentMetadata: map[string]string{"FILE_NAME": "notes.txt", "FILE_SIZE": "7", "UNRESTRICTED": "not-output"}}}
		a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		a.Manager.NewClient = func(string) session.API { return f }
		args := []string{"messages", "id:u-peer", "--details", "--json", "--resolve-names"}
		if page {
			args = append(args, "--page")
		}
		if err := a.Run(args); err != nil {
			t.Fatal(err)
		}
		var result detailedHistory
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.SchemaVersion != 1 || result.Chat.Name == nil || *result.Chat.Name != "Peer" || len(result.Messages) != 1 {
			t.Fatal("missing chat details")
		}
		m := result.Messages[0]
		if m.ID != "9007199254740993" || m.ChatID != "u-peer" || m.Sender.NameStatus != "resolved" || m.Content.Attachment == nil || *m.Content.Attachment.SizeBytes != 7 {
			t.Fatalf("missing message details: %+v", m)
		}
		if strings.Contains(out.String(), "ENC_KM") || strings.Contains(out.String(), "not-output") {
			t.Fatal("metadata leaked")
		}
		if page && (result.Order != "newest_first" || result.NextCursor == "" || result.Exhausted == nil || *result.Exhausted) {
			t.Fatal("page contract lost")
		}
		out.Reset()
		f.batches = nil
		if err := a.Run([]string{"messages", "id:u-peer", "--json"}); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out.String(), "[") || strings.Contains(out.String(), "content\"") || len(f.batches) != 0 {
			t.Fatal("legacy output/requests changed")
		}
	}
}
func TestDetailedHistoryPartialFailureAndUnavailableNames(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &chatAPI{nameError: errors.New("synthetic lookup failure")}
	f.history = []*line.Message{{ID: "1", From: "u-peer", To: "u-self", Text: "hello"}, {ID: "2", From: "u-peer", To: "u-self", ContentType: 14, ContentMetadata: map[string]string{"e2eeVersion": "2", "FILE_NAME": "untrusted"}}}
	a.Manager.NewClient = func(string) session.API { return f }
	err := a.Run([]string{"messages", "id:u-peer", "--json", "--details", "--resolve-names"})
	if err == nil {
		t.Fatal("missing partial failure")
	}
	var result detailedHistory
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Messages) != 2 {
		t.Fatal("lost partial results")
	}
	if result.Chat.NameStatus != "unavailable" || result.Messages[0].Sender.Name != nil || result.Messages[1].Status != "decryption_failed" || strings.Contains(out.String(), "untrusted") {
		t.Fatal("incorrect failure/name projection")
	}
}
func TestDetailedNamesOptionalAndBatched(t *testing.T) {
	a, _, _ := testApp(nil)
	f := &chatAPI{}
	a.Manager.NewClient = func(string) session.API { return f }
	messages := make([]messaging.Message, 205)
	for i := range messages {
		messages[i].From = fmt.Sprintf("u-%d", i)
	}
	names, err := a.senderNames(messages)
	if err != nil || len(names) != 205 || fmt.Sprint(f.batches) != "[100 100 5]" {
		t.Fatalf("names not batched: %v %v", f.batches, err)
	}
	f.batches = nil
	result, err := a.detailedMessages("u-peer", messages, nil, false)
	if err != nil || result.Messages[0].Sender.NameStatus != "not_requested" || fmt.Sprint(f.batches) != "[1]" {
		t.Fatal("optional names requested")
	}
}
func TestDetailedChatsKeepUnknownsExplicit(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &chatAPI{names: map[string]string{"u-peer": "Peer"}, boxes: []line.MessageBox{{ID: "u-peer", UnreadCount: "3", LastDeliveredMessageID: &line.MessageIDWrapper{MessageID: "9007199254740993", DeliveredTime: "42"}}, {ID: "c-group"}}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"chats", "--json", "--details"}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int            `json:"schema_version"`
		Chats         []detailedChat `json:"chats"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || len(result.Chats) != 2 {
		t.Fatal("invalid chats")
	}
	if result.Chats[0].UpdatedAt == nil || *result.Chats[0].UpdatedAt != 42 || result.Chats[0].UnreadCount == nil || result.Chats[1].UpdatedAt != nil || result.Chats[1].UnreadCount != nil || result.Chats[1].NameStatus != "unavailable" {
		t.Fatalf("incorrect unknowns: %s", out.String())
	}
	out.Reset()
	f.nameError = errors.New("names unavailable")
	if err := a.Run([]string{"chats", "--json", "--details"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name_status": "unavailable"`) {
		t.Fatal("name failure not explicit")
	}
}

func TestEnrichmentPreservesStorageAndCancellation(t *testing.T) {
	for _, err := range []error{session.ErrHostConsent, session.ErrSessionInvalidated, context.Canceled, context.DeadlineExceeded, errors.New("local store failure")} {
		if !errors.Is(enrichmentError(err), err) {
			t.Fatal("lost mandatory failure", err)
		}
	}
	a, _, _ := testApp(nil)
	a.Manager.Store = &failingDetailStore{}
	if _, err := a.detailedMessages("u-peer", nil, nil, false); !errors.Is(err, session.ErrHostConsent) {
		t.Fatalf("lost storage error: %v", err)
	}
	if _, err := a.senderNames([]messaging.Message{{From: "u-peer"}}); !errors.Is(err, session.ErrHostConsent) {
		t.Fatalf("lost sender storage error: %v", err)
	}
}

type failingDetailStore struct{ testStore }

func (*failingDetailStore) Load() (*session.State, error) { return nil, session.ErrHostConsent }

func TestDecodedPreviewDoesNotUseOuterFileName(t *testing.T) {
	m := messaging.Message{ContentType: 14, FileName: "untrusted", Content: messaging.Content{Kind: "file", Status: "ok", Attachment: &messaging.AttachmentInfo{Type: "file"}}}
	if got := messagePreview(m); got != "[File: (name unavailable)]" {
		t.Fatalf("untrusted filename fallback: %s", got)
	}
	m = messaging.Message{ContentType: 17, Content: messaging.Content{Kind: "rich", Status: "unsupported", Rich: &messaging.RichContent{}}}
	if got := messagePreview(m); got == "" {
		t.Fatal("unsupported rich message disappeared")
	}
}

func TestPostNotificationReadableAndDetailedRendering(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &chatAPI{
		names: map[string]string{"u-alice": "Alice", "u-bob": "Bob", "u-carol": "Carol", "c1234": "Weekend Trip"},
	}
	f.history = []*line.Message{
		{
			ID: "5001", From: "u-alice", To: "c1234", ToType: 2, CreatedTime: "1795673400000", ContentType: 16,
			ContentMetadata: map[string]string{
				"serviceType": "GB",
				"text":        "Packing checklist\nTent, stove, headlamps",
				"postEndUrl":  "https://line.me/R/group/home/posts/post?homeId=c1234&postId=119988",
			},
		},
		{
			ID: "5002", From: "u-bob", To: "c1234", ToType: 2, CreatedTime: "1795673700000", ContentType: 16,
			ContentMetadata: map[string]string{
				"serviceType":   "AB",
				"albumName":     "Summit photos",
				"postEndUrl":    "line://group/home/albums/album?albumId=123&albumIdV2=abc-v2",
				"chatId":        "c1234",
				"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},"bad-entry"]`,
			},
		},
		{
			ID: "5003", From: "u-carol", To: "c1234", ToType: 2, CreatedTime: "1795673880000", ContentType: 16,
		},
		{
			ID: "5004", From: "u-alice", To: "c1234", ToType: 2, CreatedTime: "1795674000000", ContentType: 0,
			Text: "Alice created a note fallback sentence.",
			ContentMetadata: map[string]string{
				"ORGCONTP":    "POSTNOTIFICATION",
				"serviceType": "GB",
				"text":        "Shared reminder\nDon't forget sunscreen",
				"postEndUrl":  "https://line.me/R/group/home/posts/post?homeId=c1234&postId=119990",
			},
		},
	}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }

	// Readable messages output
	if err := a.Run([]string{"messages", "id:c1234"}); err != nil {
		t.Fatal(err)
	}
	readable := out.String()
	for _, want := range []string{
		"       [Note: Packing checklist]\n       Tent, stove, headlamps\n       https://line.me/R/group/home/posts/post?homeId=c1234&postId=119988\n",
		"       [Album: Summit photos]\n       line://group/home/albums/album?albumId=123&albumIdV2=abc-v2\n",
		"       [Post notification]\n",
		"       [Note: Shared reminder]\n       Don't forget sunscreen\n       https://line.me/R/group/home/posts/post?homeId=c1234&postId=119990\n",
	} {
		if !strings.Contains(readable, want) {
			t.Fatalf("readable missing %q in:\n%s", want, readable)
		}
	}
	if strings.Contains(readable, "fallback sentence") || strings.Contains(readable, "[Malformed message content]") {
		t.Fatalf("readable leaked fallback sentence or hid retained fields:\n%s", readable)
	}

	// Detailed JSON output
	out.Reset()
	if err := a.Run([]string{"messages", "id:c1234", "--details", "--json", "--resolve-names"}); err != nil {
		t.Fatal(err)
	}
	var detailed detailedHistory
	if err := json.Unmarshal(out.Bytes(), &detailed); err != nil {
		t.Fatal(err)
	}
	if len(detailed.Messages) != 4 {
		t.Fatalf("detailed count = %d", len(detailed.Messages))
	}
	if detailed.Messages[0].Content.Kind != "post_notification" || detailed.Messages[0].Text != "" || detailed.Messages[0].Content.Post.PreviewCount != nil {
		t.Fatalf("detailed note: %+v", detailed.Messages[0])
	}
	if detailed.Messages[1].Status != "malformed_metadata" || detailed.Messages[1].Content.Post.Title != "Summit photos" || detailed.Messages[1].Content.Post.AlbumID != "abc-v2" || *detailed.Messages[1].Content.Post.PreviewCount != 1 {
		t.Fatalf("detailed partial album: %+v", detailed.Messages[1])
	}
	if detailed.Messages[3].ContentType != 0 || detailed.Messages[3].Text != "" || detailed.Messages[3].Content.Kind != "post_notification" || detailed.Messages[3].Content.Post.Type != "note" {
		t.Fatalf("detailed wrapper note: %+v", detailed.Messages[3])
	}
	if strings.Contains(out.String(), "oid-1") || strings.Contains(out.String(), "fallback sentence") {
		t.Fatalf("detailed JSON leaked private/fallback data:\n%s", out.String())
	}

	// Legacy JSON preserves original wire projection
	out.Reset()
	if err := a.Run([]string{"messages", "id:c1234", "--json"}); err != nil {
		t.Fatal(err)
	}
	var legacy []messaging.Message
	if err := json.Unmarshal(out.Bytes(), &legacy); err != nil || len(legacy) != 4 {
		t.Fatalf("legacy decode: %v", err)
	}
	if legacy[0].Status != "unsupported" || legacy[0].Text != "" || legacy[3].Status != "plaintext" || legacy[3].Text != "Alice created a note fallback sentence." {
		t.Fatalf("legacy projection changed: %+v", legacy)
	}
}
