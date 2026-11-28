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
	if _, ok := decodeMentions(`{"MENTIONEES":[]}`, strings.Repeat("x", maxContentBytes+1)); ok {
		t.Fatal("unbounded mention text accepted")
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
		got := decodeContent("u-peer", &tc.msg, false, "")
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

func TestWireLocationMissingCoordinatesNeverBecomesZero(t *testing.T) {
	for _, raw := range []string{`{}`, `{"latitude":null,"longitude":1}`, `{"latitude":2}`, `{"latitude":"wrong","longitude":2}`} {
		var msg line.Message
		if err := json.Unmarshal([]byte(`{"contentType":15,"location":`+raw+`}`), &msg); err != nil {
			t.Fatal(err)
		}
		if got := decodeContent("u-peer", &msg, false, ""); got.Status != "malformed_metadata" || got.Location != nil {
			t.Fatal("invented location", got)
		}
		if got := decodeContent("u-peer", &line.Message{}, true, `{"location":`+raw+`}`); got.Status != "malformed_metadata" || got.Location != nil {
			t.Fatal("invented encrypted location", got)
		}
	}
	var msg line.Message
	if err := json.Unmarshal([]byte(`{"contentType":15,"location":{"latitude":0,"longitude":0}}`), &msg); err != nil {
		t.Fatal(err)
	}
	if got := decodeContent("u-peer", &msg, false, ""); got.Status != "ok" || got.Location == nil {
		t.Fatal("rejected actual zero coordinates")
	}
}

func TestAttachmentEncryptionMarkersDoNotPermitPlaintextProjection(t *testing.T) {
	for key, value := range map[string]string{"e2eeVersion": "2", "SID": "emf", "ENC_KM": "secret", "OID": "object"} {
		c, f, crypto, _ := setup(t, true)
		raw := &line.Message{ContentType: 14, ContentMetadata: map[string]string{key: value, "FILE_NAME": "outer fallback"}}
		got := c.Decode("u-peer", raw)
		if !got.Encrypted || got.Content.Status != "decryption_failed" || got.Content.Attachment != nil || crypto.decryptCalls != 0 || f.sends != 0 || f.registrations != 0 {
			t.Fatalf("unsafe marker %s: %+v", key, got.Content)
		}
	}
}

func TestPostNotificationNativeTypesAndWrappers(t *testing.T) {
	c, _, _, _ := setup(t, true)

	// Native type 16 Note (multiline, GB)
	noteMsg := &line.Message{
		ID: "5001", From: "u-alice", To: "c1234", ToType: 2, ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   " gb ",
			"text":          "Packing checklist\nTent, stove, headlamps",
			"postEndUrl":    "https://line.me/R/group/home/posts/post?homeId=c1234&postId=119988",
			"albumName":     strings.Repeat("x", maxContentBytes+1), // ignored on Notes
			"chatId":        "invalid chat id",                      // ignored on Notes
			"previewMedias": "{bad json",                            // ignored on Notes
			"ENC_KM":        "must-not-leak",
			"UNKNOWN_KEY":   "hidden-meta",
		},
	}
	gotNote := c.Decode("c1234", noteMsg)
	if gotNote.Status != "unsupported" || gotNote.Text != "" || gotNote.Encrypted {
		t.Fatalf("native 16 legacy projection changed: %+v", gotNote)
	}
	if gotNote.Content.Kind != "post_notification" || gotNote.Content.Status != "ok" || gotNote.Content.Text != "" || gotNote.Content.Post == nil {
		t.Fatalf("note decode: %+v", gotNote.Content)
	}
	p := gotNote.Content.Post
	if p.Type != "note" || p.Title != "" || p.Text != "Packing checklist\nTent, stove, headlamps" || p.URL != noteMsg.ContentMetadata["postEndUrl"] || p.AlbumID != "" || p.PreviewCount != nil {
		t.Fatalf("note post fields: %+v", p)
	}
	if IsDownloadableMessage(gotNote) {
		t.Fatal("note must not be downloadable")
	}

	// Native type 16 Album with albumIdV2 preferred over albumId, and same-chat previews
	albumMsg := &line.Message{
		ID: "5002", From: "u-bob", To: "c1234", ToType: 2, ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"albumName":     "  Summit photos  ",
			"postEndUrl":    "line://group/home/albums/album?albumId=123&albumIdV2=abc-v2",
			"chatId":        "c1234",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"vid-1","mediaType":"V"},{"svc":"album","sid":"a","mediaOid":"oid-2","mediaType":"I"}]`,
			"ENC_KM":        "secret-key",
		},
	}
	gotAlbum := c.Decode("c1234", albumMsg)
	if gotAlbum.Content.Kind != "post_notification" || gotAlbum.Content.Status != "ok" || gotAlbum.Content.Post == nil {
		t.Fatalf("album decode: %+v", gotAlbum.Content)
	}
	ap := gotAlbum.Content.Post
	if ap.Type != "album" || ap.Title != "Summit photos" || ap.AlbumID != "abc-v2" || ap.PreviewCount == nil || *ap.PreviewCount != 2 {
		t.Fatalf("album post fields: %+v", ap)
	}
	if !IsDownloadableMessage(gotAlbum) {
		t.Fatal("same-chat album with previews should be downloadable")
	}
	// Allowlist leak check
	marshaled, _ := json.Marshal(gotAlbum.Content)
	for _, secret := range []string{"oid-1", "oid-2", "vid-1", "c1234", "ENC_KM", "secret-key", "previewMedias"} {
		if strings.Contains(string(marshaled), secret) {
			t.Fatalf("leaked private metadata %q in %s", secret, marshaled)
		}
	}

	// Fallback to albumId when albumIdV2 is absent
	albumV1 := &line.Message{
		ID: "5003", ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType": "AB",
			"albumName":   "Legacy Album",
			"postEndUrl":  "https://line.me/R/group/home/albums/album?albumId=789",
		},
	}
	gotV1 := c.Decode("c1234", albumV1)
	if gotV1.Content.Status != "ok" || gotV1.Content.Post.AlbumID != "789" || gotV1.Content.Post.PreviewCount == nil || *gotV1.Content.Post.PreviewCount != 0 {
		t.Fatalf("album v1 decode: %+v", gotV1.Content)
	}

	// Unknown serviceType and nil metadata
	for _, meta := range []map[string]string{nil, {}, {"serviceType": "XY", "text": "Hello"}} {
		got := c.Decode("c1234", &line.Message{ID: "5004", ContentType: 16, ContentMetadata: meta})
		if got.Content.Kind != "post_notification" || got.Content.Status != "ok" || got.Content.Post == nil || got.Content.Post.Type != "unknown" || got.Content.Post.PreviewCount != nil {
			t.Fatalf("unknown/nil decode: %+v", got.Content)
		}
	}

	// Plaintext type 0 ORGCONTP wrapper: byte-for-byte legacy JSON + detailed projection
	wrapMsg := &line.Message{
		ID: "5005", From: "u-alice", To: "c1234", ContentType: 0,
		Text: "Alice created a note.",
		ContentMetadata: map[string]string{
			"ORGCONTP":    "POSTNOTIFICATION",
			"serviceType": "GB",
			"text":        "Shared reminder",
			"postEndUrl":  "https://line.me/R/group/home/posts/post?homeId=c1234&postId=119990",
		},
	}
	gotWrap := c.Decode("c1234", wrapMsg)
	legacyData, _ := json.Marshal(gotWrap)
	wantLegacy := `{"id":"5005","from":"u-alice","to":"c1234","created_time":0,"content_type":0,"text":"Alice created a note.","encrypted":false,"status":"plaintext"}`
	if string(legacyData) != wantLegacy {
		t.Fatalf("wrapper legacy JSON changed:\ngot:  %s\nwant: %s", legacyData, wantLegacy)
	}
	if gotWrap.Content.Kind != "post_notification" || gotWrap.Content.Status != "ok" || gotWrap.Content.Text != "" || gotWrap.Content.Post == nil || gotWrap.Content.Post.Text != "Shared reminder" {
		t.Fatalf("wrapper detailed decode: %+v", gotWrap.Content)
	}

	// Plaintext type 0 with Location + ORGCONTP preserves location precedence
	locWrap := &line.Message{
		ID: "5006", ContentType: 0,
		Location:        &line.MessageLocation{Title: "Park", Address: "Main St", Latitude: 25, Longitude: 121},
		ContentMetadata: map[string]string{"ORGCONTP": "POSTNOTIFICATION", "serviceType": "GB", "text": "spoofed"},
	}
	gotLoc := c.Decode("c1234", locWrap)
	if gotLoc.Content.Kind != "location" || gotLoc.Content.Post != nil || gotLoc.Content.Location == nil {
		t.Fatalf("location precedence broken: %+v", gotLoc.Content)
	}
}

func TestPostNotificationE2EEAndAttachmentBoundaries(t *testing.T) {
	// Encrypted type 0 with ORGCONTP: decryption success uses authenticated text, never outer post
	c, _, crypto, _ := setup(t, false)
	crypto.payload = `{"text":"authenticated hello"}`
	encWrap := &line.Message{
		ID: "6001", From: "u-peer", To: "u-self", ContentType: 0,
		Text:   "outer fallback",
		Chunks: encryptedChunks(22, 11),
		ContentMetadata: map[string]string{
			"e2eeVersion":   "2",
			"ORGCONTP":      "POSTNOTIFICATION",
			"serviceType":   "AB",
			"albumName":     "Spoofed Album",
			"chatId":        "u-peer",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	got := c.Decode("u-peer", encWrap)
	if got.Status != "decrypted" || got.Text != "authenticated hello" || got.Content.Kind != "text" || got.Content.Status != "ok" || got.Content.Post != nil || IsDownloadableMessage(got) {
		t.Fatalf("encrypted type 0 wrapper leaked post: %+v", got)
	}

	// Encrypted type 0 with ORGCONTP: decryption failure is never bypassed
	crypto.decryptErr = errors.New("decrypt boom")
	gotFail := c.Decode("u-peer", encWrap)
	if gotFail.Status != "decryption_failed" || gotFail.Error == "" || gotFail.Content.Status != "decryption_failed" || gotFail.Content.Post != nil || IsDownloadableMessage(gotFail) {
		t.Fatalf("encrypted type 0 failure bypassed: %+v", gotFail)
	}
	crypto.decryptErr = nil

	// Native encrypted type 16 (chunks or e2eeVersion) -> kind post_notification, status unsupported, no post
	for _, meta := range []struct {
		name   string
		chunks []string
		ver    string
	}{
		{"chunks", encryptedChunks(22, 11), ""},
		{"e2eeVersion", nil, "2"},
	} {
		t.Run("encrypted-native-16-"+meta.name, func(t *testing.T) {
			beforeCalls := crypto.decryptCalls
			msg := &line.Message{
				ID: "6002", From: "u-peer", To: "u-self", ContentType: 16,
				Chunks: meta.chunks,
				ContentMetadata: map[string]string{
					"e2eeVersion":   meta.ver,
					"serviceType":   "AB",
					"albumName":     "Hidden",
					"chatId":        "u-peer",
					"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
				},
			}
			res := c.Decode("u-peer", msg)
			if !res.Encrypted || res.Status != "unsupported" || res.Text != "" || res.Content.Kind != "post_notification" || res.Content.Status != "unsupported" || res.Content.Post != nil || IsDownloadableMessage(res) {
				t.Fatalf("encrypted native 16 unsafe: %+v", res)
			}
			if crypto.decryptCalls != beforeCalls {
				t.Fatal("attempted to decrypt unsupported native type 16")
			}
		})
	}

	// ORGCONTP on encrypted attachment (ContentType 14) stays an attachment
	crypto.payload = `{"fileName":"doc.pdf","keyMaterial":"k"}`
	att := &line.Message{
		ID: "6003", From: "u-peer", To: "u-self", ContentType: 14,
		Chunks: encryptedChunks(22, 11),
		ContentMetadata: map[string]string{
			"ORGCONTP":      "POSTNOTIFICATION",
			"serviceType":   "AB",
			"albumName":     "Spoofed",
			"chatId":        "u-peer",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	gotAtt := c.Decode("u-peer", att)
	if gotAtt.Content.Kind != "file" || gotAtt.Content.Status != "ok" || gotAtt.Content.Post != nil || gotAtt.Content.Attachment == nil || *gotAtt.Content.Attachment.Name != "doc.pdf" {
		t.Fatalf("attachment diverted to post: %+v", gotAtt.Content)
	}
}

func TestPostNotificationURLAndQueryAllowlist(t *testing.T) {
	cases := []struct {
		name        string
		serviceType string
		rawURL      string
		wantStatus  string
		wantURL     string
		wantAlbumID string
	}{
		{"https note post", "GB", "https://line.me/R/group/home/posts/post?homeId=c1234&postId=99", "ok", "https://line.me/R/group/home/posts/post?homeId=c1234&postId=99", ""},
		{"https note post with noteEntryType", "GB", "https://line.me/R/group/home/posts/post?homeId=c1234&noteEntryType=post_noti&postId=99", "ok", "https://line.me/R/group/home/posts/post?homeId=c1234&noteEntryType=post_noti&postId=99", ""},
		{"https home post", "GB", "https://line.me/R/home/public/post?postId=99&source=talk", "ok", "https://line.me/R/home/public/post?postId=99&source=talk", ""},
		{"line group album", "AB", "line://group/home/albums/album?albumId=12&albumIdV2=v2-id", "ok", "line://group/home/albums/album?albumId=12&albumIdV2=v2-id", "v2-id"},
		{"line home post", "GB", "line://home/posts/post?postId=55", "ok", "line://home/posts/post?postId=55", ""},
		{"unknown query param omits url with ok", "GB", "https://line.me/R/group/home/posts/post?postId=99&utm=1", "ok", "", ""},
		{"unknown query param on album omits url but keeps album_id", "AB", "line://group/home/albums/album?albumId=12&albumIdV2=v2-ok&extra=1", "ok", "", "v2-ok"},
		{"unrecognized line.me route omits url with ok", "GB", "https://line.me/ti/p/abc", "ok", "", ""},
		{"unrecognized line:// authority omits url with ok", "GB", "line://nv/profile/abc", "ok", "", ""},
		{"album_id not extracted on Note", "GB", "line://group/home/albums/album?albumId=12&albumIdV2=v2-id", "ok", "line://group/home/albums/album?albumId=12&albumIdV2=v2-id", ""},
		{"album_id not extracted on post route", "AB", "https://line.me/R/group/home/posts/post?albumId=12", "ok", "https://line.me/R/group/home/posts/post?albumId=12", ""},
		{"http rejected", "AB", "http://line.me/R/group/home/albums/album?albumId=12", "malformed_metadata", "", ""},
		{"javascript rejected", "GB", "javascript:alert(1)", "malformed_metadata", "", ""},
		{"relative rejected", "GB", "/R/group/home/posts/post?postId=1", "malformed_metadata", "", ""},
		{"port rejected", "GB", "https://line.me:443/R/group/home/posts/post?postId=1", "malformed_metadata", "", ""},
		{"userinfo rejected", "GB", "https://user:pass@line.me/R/group/home/posts/post?postId=1", "malformed_metadata", "", ""},
		{"non-line host rejected", "GB", "https://evil.example/R/group/home/posts/post?postId=1", "malformed_metadata", "", ""},
		{"invalid percent escaping rejected", "AB", "line://group/home/albums/album?albumId=%ZZ", "malformed_metadata", "", ""},
		{"control char rejected", "GB", "https://line.me/R/group/home/posts/post?postId=1\n2", "malformed_metadata", "", ""},
		{"whitespace rejected", "GB", "https://line.me/R/group/home/posts/post?postId=1 2", "malformed_metadata", "", ""},
		{"embedded credentials in query rejected", "GB", "https://line.me/R/group/home/posts/post?postId=u@host", "malformed_metadata", "", ""},
		{"duplicate albumId rejected", "AB", "line://group/home/albums/album?albumId=1&albumId=2", "malformed_metadata", "line://group/home/albums/album?albumId=1&albumId=2", ""},
		{"overlong albumIdV2 rejected", "AB", "line://group/home/albums/album?albumIdV2=" + strings.Repeat("a", 257), "malformed_metadata", "line://group/home/albums/album?albumIdV2=" + strings.Repeat("a", 257), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &line.Message{
				ContentType: 16,
				ContentMetadata: map[string]string{
					"serviceType": tc.serviceType,
					"postEndUrl":  tc.rawURL,
				},
			}
			got := decodeContent("c1234", msg, false, "")
			if got.Status != tc.wantStatus || got.Post == nil || got.Post.URL != tc.wantURL || got.Post.AlbumID != tc.wantAlbumID {
				t.Fatalf("got status=%s url=%q album_id=%q; want status=%s url=%q album_id=%q",
					got.Status, got.Post.URL, got.Post.AlbumID, tc.wantStatus, tc.wantURL, tc.wantAlbumID)
			}
		})
	}
}

func TestPostNotificationPreviewMediasAndChatBinding(t *testing.T) {
	// No top-level mediaOid fallback when previewMedias is absent or empty
	noFallback := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType": "AB",
			"albumName":   "Photos",
			"chatId":      "c1234",
			"mediaOid":    "top-level-oid",
			"mediaType":   "I",
		},
	}
	got := decodeContent("c1234", noFallback, false, "")
	if got.Status != "ok" || got.Post.PreviewCount == nil || *got.Post.PreviewCount != 0 {
		t.Fatalf("top-level mediaOid fallback must not be used: %+v", got)
	}

	// Mixed valid/malformed previewMedias retains valid siblings in order and reports malformed_metadata
	mixed := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType": "AB",
			"albumName":   "Mixed",
			"chatId":      "c1234",
			"previewMedias": `[
				{"svc":"album","sid":"a","mediaOid":"good-1","mediaType":"I"},
				"bad-shape",
				{"svc":"album","sid":"a","mediaOid":"../escape","mediaType":"I"},
				{"svc":"album","sid":"a","mediaOid":"good-2","mediaType":"I"},
				{"svc":"album","sid":"a","mediaOid":"good-1","mediaType":"I"},
				{"svc":"album","sid":"a","mediaOid":"vid-1","mediaType":"V"}
			]`,
		},
	}
	parsed := parsePostNotification("c1234", mixed)
	if parsed.status != "malformed_metadata" || parsed.post.Title != "Mixed" || parsed.post.PreviewCount == nil || *parsed.post.PreviewCount != 2 {
		t.Fatalf("mixed preview parse: %+v", parsed)
	}
	if strings.Join(parsed.oids, ",") != "good-1,good-2" {
		t.Fatalf("retained oids = %v, want [good-1 good-2]", parsed.oids)
	}

	// Cap > 64 entries -> malformed_metadata, preview_count 0, title preserved
	entries := make([]string, 65)
	for i := range entries {
		entries[i] = `{"svc":"album","sid":"a","mediaOid":"oid-` + string(rune('a'+i%26)) + `","mediaType":"I"}`
	}
	overCap := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"albumName":     "Huge",
			"chatId":        "c1234",
			"previewMedias": "[" + strings.Join(entries, ",") + "]",
		},
	}
	gotOver := decodeContent("c1234", overCap, false, "")
	if gotOver.Status != "malformed_metadata" || gotOver.Post.Title != "Huge" || *gotOver.Post.PreviewCount != 0 {
		t.Fatalf("over-cap previews: %+v", gotOver)
	}

	// Invalid JSON, null, non-array -> malformed_metadata
	for _, bad := range []string{`null`, `{}`, `[bad`, strings.Repeat(" ", maxContentBytes+1)} {
		m := &line.Message{ContentType: 16, ContentMetadata: map[string]string{"serviceType": "AB", "chatId": "c1234", "previewMedias": bad}}
		g := decodeContent("c1234", m, false, "")
		if g.Status != "malformed_metadata" || *g.Post.PreviewCount != 0 {
			t.Fatalf("bad previewMedias %q accepted: %+v", bad[:min(len(bad), 20)], g)
		}
	}

	// Cross-chat album: readable, linkable, status ok, preview_count 0
	crossChat := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"albumName":     "Other Group Album",
			"postEndUrl":    "line://group/home/albums/album?albumId=55",
			"chatId":        "c-other",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	gotCross := decodeContent("c-selected", crossChat, false, "")
	if gotCross.Status != "ok" || gotCross.Post.Title != "Other Group Album" || gotCross.Post.URL == "" || *gotCross.Post.PreviewCount != 0 {
		t.Fatalf("cross-chat album: %+v", gotCross)
	}

	// Missing chatId: status ok, preview_count 0
	noChat := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	gotNoChat := decodeContent("c-selected", noChat, false, "")
	if gotNoChat.Status != "ok" || *gotNoChat.Post.PreviewCount != 0 {
		t.Fatalf("missing chatId album: %+v", gotNoChat)
	}

	// Syntactically invalid chatId: malformed_metadata, preview_count 0
	badChat := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"chatId":        "not a mid!",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	gotBadChat := decodeContent("c-selected", badChat, false, "")
	if gotBadChat.Status != "malformed_metadata" || *gotBadChat.Post.PreviewCount != 0 {
		t.Fatalf("invalid chatId album: %+v", gotBadChat)
	}

	// V2 selected group chat with legacy V1 hex chatId and matching postEndUrl homeId -> bound
	v2Chat := "C" + strings.Repeat("a", 43)
	v2Other := "C" + strings.Repeat("b", 43)
	v1Group := "c" + strings.Repeat("1", 32)
	v1User := "u" + strings.Repeat("1", 32)
	v2Album := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"albumName":     "V2 Group Album",
			"postEndUrl":    "line://group/home/albums/album?albumId=123&homeId=" + v2Chat + "&albumIdV2=123",
			"chatId":        v1Group,
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-2","mediaType":"I"}]`,
		},
	}
	parsedV2 := parsePostNotification(v2Chat, v2Album)
	if parsedV2.status != "ok" || parsedV2.chatID != v1Group || parsedV2.post.PreviewCount == nil || *parsedV2.post.PreviewCount != 2 || len(parsedV2.oids) != 2 {
		t.Fatalf("V2 group album binding failed: %+v", parsedV2)
	}
	// V2 group chat with cross-chat homeId -> preview_count 0
	v2Cross := parsePostNotification(v2Other, v2Album)
	if v2Cross.status != "ok" || *v2Cross.post.PreviewCount != 0 || len(v2Cross.oids) != 0 {
		t.Fatalf("V2 cross-chat album must not bind: %+v", v2Cross)
	}
	// V2 group chat with missing homeId -> preview_count 0
	v2NoHome := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"postEndUrl":    "line://group/home/albums/album?albumId=123&albumIdV2=123",
			"chatId":        v1Group,
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	if p := parsePostNotification(v2Chat, v2NoHome); p.status != "ok" || *p.post.PreviewCount != 0 {
		t.Fatalf("V2 album without homeId must not bind: %+v", p)
	}
	// V2 group chat with mismatched chat type V1 user MID -> preview_count 0
	v2WrongType := &line.Message{
		ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"postEndUrl":    "line://group/home/albums/album?albumId=123&homeId=" + v2Chat + "&albumIdV2=123",
			"chatId":        v1User,
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	if p := parsePostNotification(v2Chat, v2WrongType); p.status != "ok" || *p.post.PreviewCount != 0 {
		t.Fatalf("V2 group album with V1 user chatId must not bind: %+v", p)
	}
}
