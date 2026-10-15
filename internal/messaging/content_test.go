package messaging

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
)

func TestDecodedEncryptedContentUsesAuthenticatedFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    int
		payload string
	}{
		{"file", 14, `{"fileName":"private.txt","keyMaterial":"secret-key","unrestricted":"hidden"}`},
		{"location", 0, `{"location":{"title":"Park","address":"Lane","latitude":25,"longitude":121}}`},
		{"mentions", 0, `{"text":"😀 @Peer"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, crypto, _ := setup(t, false)
			crypto.payload = tc.payload
			f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
			msg := &line.Message{ID: "9007199254740993", From: "u-peer", To: "c-group", ToType: 2, ContentType: tc.kind, Text: "outer secret", Location: &line.MessageLocation{Title: "outer"}, Chunks: encryptedChunks(22, 33), ContentMetadata: map[string]string{"FILE_NAME": "outer-secret", "MENTION": `{"MENTIONEES":[{"S":"3","E":"8","M":"u-peer"}]}`, "ENC_KM": "secret-key"}}
			if tc.name != "mentions" {
				delete(msg.ContentMetadata, "MENTION")
			}
			got := c.Decode("c-group", msg)
			if got.ChatID != "c-group" || got.Content.Status != "ok" {
				t.Fatalf("bad decode: %+v", got.Content)
			}
			if tc.name == "file" && (got.Content.Attachment.Name == nil || *got.Content.Attachment.Name != "private.txt" || got.Content.Attachment.SizeBytes != nil) {
				t.Fatal("filename/unknown size lost")
			}
			if tc.name == "location" && (got.Content.Location == nil || got.Content.Location.Title != "Park") {
				t.Fatal("location lost")
			}
			if tc.name == "mentions" && (len(got.Content.Mentions) != 1 || got.Content.Mentions[0].Start != 3) {
				t.Fatal("mention lost")
			}
			data, _ := json.Marshal(got.Content)
			for _, secret := range []string{"outer", "secret-key", "hidden", "keyMaterial", "chunks"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
			if f.registrations != 0 || f.sends != 0 {
				t.Fatal("read mutated account")
			}
		})
	}
}

func TestDecodedContentFailureAndMissingName(t *testing.T) {
	c, _, crypto, _ := setup(t, false)
	raw := &line.Message{From: "u-peer", To: "u-self", ContentType: 14, Chunks: encryptedChunks(22, 11), ContentMetadata: map[string]string{"FILE_NAME": "untrusted"}}
	crypto.payload = `{"keyMaterial":"secret"}`
	got := c.Decode("u-peer", raw)
	if got.Content.Status != "ok" || got.Content.Attachment.Name != nil {
		t.Fatal("fell back to outer filename")
	}
	crypto.payload = `{"fileName":42}`
	if c.Decode("u-peer", raw).Content.Status != "malformed_metadata" {
		t.Fatal("malformed authenticated body accepted")
	}
	crypto.decryptErr = errors.New("secret failure")
	got = c.Decode("u-peer", raw)
	if got.Content.Status != "decryption_failed" || got.Content.Attachment != nil {
		t.Fatal("decryption failure fell back")
	}
	raw.ContentType = 99
	if c.Decode("u-peer", raw).Content.Status != "unsupported" {
		t.Fatal("unknown type lost")
	}
}

func TestMentionsValidateUTF16AndBounds(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"MENTIONEES":[{"S":"1","E":"2","M":"u-peer"}]}`,
		`{"MENTIONEES":[{"S":"0","E":"99","M":"u-peer"}]}`,
		`{"MENTIONEES":[{"S":"0","E":"2","M":"u-peer"},{"S":"0","E":"2","A":"1"}]}`,
		`{"MENTIONEES":[{"S":"0","E":"2","M":"bad"}]}`,
		strings.Repeat("x", maxContentBytes+1),
	} {
		if _, ok := decodeMentions(raw, "😀 @Peer"); ok {
			t.Fatal("accepted malformed mentions")
		}
	}
	mentions, ok := decodeMentions(`{"MENTIONEES":[{"S":"3","E":"8","A":"1"}]}`, "😀 @Peer")
	if !ok || len(mentions) != 1 || !mentions[0].All {
		t.Fatal("mention all lost")
	}
}

func TestAllowlistedContentAndRichBounds(t *testing.T) {
	cases := []struct {
		msg          line.Message
		kind, status string
	}{
		{line.Message{ContentType: 15, Location: &line.MessageLocation{Latitude: 25, Longitude: 121}}, "location", "ok"},
		{line.Message{ContentType: 15, Location: &line.MessageLocation{Latitude: 100}}, "location", "malformed_metadata"},
		{line.Message{ContentType: 15}, "location", "malformed_metadata"},
		{line.Message{ContentType: 13, ContentMetadata: map[string]string{"mid": "u-peer", "displayName": "Peer"}}, "contact", "ok"},
		{line.Message{ContentType: 7, ContentMetadata: map[string]string{"STKID": "9007199254740993", "STKPKGID": "123"}}, "sticker", "ok"},
		{line.Message{ContentType: 2, ContentMetadata: map[string]string{"DURATION": "3000", "FILE_SIZE": "-1"}}, "video", "malformed_metadata"},
		{line.Message{ContentType: 17}, "rich", "unsupported"},
	}
	for _, tc := range cases {
		got := decodeContent(&tc.msg, false, "")
		if got.Kind != tc.kind || got.Status != tc.status {
			t.Fatalf("got %+v", got)
		}
	}
	rich, ok := decodeRich("Fallback", `{"type":"bubble","body":{"type":"box","contents":[{"type":"text","text":"Hello","keyMaterial":"secret"},{"type":"button","action":{"type":"uri","label":"Visit","uri":"https://example.com/"}},{"type":"image","url":"https://secret.example/"},{"type":"button","action":{"type":"uri","uri":"javascript:bad"}}]}}`)
	if !ok || strings.Join(rich.Text, " ") != "Hello Visit" || len(rich.Links) != 1 || rich.Links[0] != "https://example.com/" {
		t.Fatalf("bad rich: %+v", rich)
	}
	data, _ := json.Marshal(rich)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "javascript") {
		t.Fatal("unrestricted rich data leaked")
	}
	for _, raw := range []string{`null`, `[]`, `{`, `{"contents":3}`, strings.Repeat("x", maxContentBytes+1)} {
		if _, ok := decodeRich("", raw); ok {
			t.Fatal("invalid rich accepted")
		}
	}
	deep := `{"type":"text","text":"too deep"}`
	for i := 0; i < 20; i++ {
		deep = `{"type":"box","contents":[` + deep + `]}`
	}
	rich, ok = decodeRich("", deep)
	if !ok || !rich.Truncated || len(rich.Text) != 0 {
		t.Fatal("depth not bounded")
	}
	wide := `{"type":"box","contents":[` + strings.Repeat(`{"type":"text","text":"hello"},`, 300) + `{"type":"text","text":"end"}]}`
	rich, ok = decodeRich("", wide)
	if !ok || !rich.Truncated || len(rich.Text) > 100 {
		t.Fatal("breadth not bounded")
	}
}

func TestLegacyJSONDoesNotAcquireDetailedFields(t *testing.T) {
	c, _, _, _ := setup(t, true)
	got := c.Decode("u-peer", &line.Message{ID: "1", Text: "hi", From: "u-peer", To: "u-self"})
	data, _ := json.Marshal(got)
	want := `{"id":"1","from":"u-peer","to":"u-self","created_time":0,"content_type":0,"text":"hi","encrypted":false,"status":"plaintext"}`
	if string(data) != want {
		t.Fatalf("legacy changed: %s", data)
	}
}
