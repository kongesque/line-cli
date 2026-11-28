package messaging

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func mediaFixture(t *testing.T, kind int, chat string, encrypted bool) (*Client, *fileAPI, *fakeCrypto, *line.Message) {
	t.Helper()
	c, f, crypto, _ := setup(t, false)
	api := &fileAPI{fakeAPI: f, data: []byte("attachment\x00\xff")}
	c.Session.NewClient = func(string) session.API { return api }
	msg := &line.Message{ID: "123", From: "u-peer", To: "u-self", ContentType: kind, ContentMetadata: map[string]string{"OBS_POP": "pop value"}}
	if chat != "u-peer" {
		msg.To, msg.ToType = chat, toType(chat)
		f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
	}
	f.history = []*line.Message{msg}
	if encrypted {
		var key string
		var err error
		api.data, key, err = encryptFile(api.data)
		if err != nil {
			t.Fatal(err)
		}
		if kind == 2 {
			material, _ := base64.StdEncoding.DecodeString(key)
			_, macKey, _, _ := fileKeys(material)
			digest := sha256.Sum256(api.data[:len(api.data)-32])
			mac := hmac.New(sha256.New, macKey)
			mac.Write(digest[:])
			copy(api.data[len(api.data)-32:], mac.Sum(nil))
		}
		payload, _ := json.Marshal(map[string]string{"keyMaterial": key})
		crypto.payload = string(payload)
		receiver := 11
		if chat != "u-peer" {
			receiver = 33
		}
		msg.Chunks = encryptedChunks(22, receiver)
		msg.ContentMetadata["OID"] = "object-id"
		msg.ContentMetadata["SID"] = attachmentSID(kind)
		msg.ContentMetadata["e2eeVersion"] = "2"
	}
	return c, api, crypto, msg
}

func TestMediaDownloadTypesAndHistoricalKeys(t *testing.T) {
	for _, tc := range []struct {
		kind int
		sid  string
	}{{1, "emi"}, {2, "emv"}, {3, "ema"}, {14, "emf"}} {
		for _, chat := range []string{"u-peer", "c-group", "r-room"} {
			for _, encrypted := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/encrypted=%v", tc.kind, chat, encrypted), func(t *testing.T) {
					c, api, crypto, msg := mediaFixture(t, tc.kind, chat, encrypted)
					if !encrypted {
						msg.ContentMetadata["MEDIA_CONTENT_INFO"] = `{"category":"original"}`
					}
					data, err := c.DownloadFile(context.Background(), chat, "123")
					if err != nil || !bytes.Equal(data, []byte("attachment\x00\xff")) {
						t.Fatalf("download = %q, %v", data, err)
					}
					wantSID, wantOID, wantMeta, wantTID := "m", "123", "", "original"
					if encrypted {
						wantSID, wantOID, wantMeta, wantTID = tc.sid, "object-id", "123", ""
					}
					if api.requestSID != wantSID || api.requestOID != wantOID || api.talkMeta != wantMeta || api.options.TID != wantTID || api.options.OBSPop != "pop value" || api.options.MaxBytes != MaxAttachmentBytes+32 {
						t.Fatalf("incorrect request: sid=%s oid=%s meta=%s options=%+v", api.requestSID, api.requestOID, api.talkMeta, api.options)
					}
					if encrypted && crypto.decryptCalls != 1 {
						t.Fatal("did not decrypt message envelope")
					}
					if encrypted && chat != "u-peer" && (len(api.groupIDs) != 1 || api.groupIDs[0] != 33 || len(crypto.unwrapped) != 1) {
						t.Fatal("historical group key not used")
					}
					if api.registrations != 0 || api.sends != 0 || api.store.state.LastReqSeq != 0 {
						t.Fatal("download mutated remote state or reserved a sequence")
					}
					decoded := c.Decode(chat, msg)
					if decoded.Status != "attachment" || decoded.Text != "" || decoded.Encrypted != encrypted {
						t.Fatalf("unsafe attachment summary: %+v", decoded)
					}
				})
			}
		}
	}
}

func TestMediaDownloadOwnDeviceEcho(t *testing.T) {
	c, api, _, msg := mediaFixture(t, 1, "u-peer", true)
	msg.From, msg.To = "u-self", "u-peer"
	msg.Chunks = encryptedChunks(10, 22)
	if _, err := c.DownloadFile(context.Background(), "u-peer", "123"); err != nil {
		t.Fatal(err)
	}
	if len(api.lookupIDs) != 1 || api.lookupIDs[0] != 22 {
		t.Fatal("wrong historical peer key")
	}
}

func TestMediaDownloadRejectsUnsafeMetadataBeforeOBS(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Client, *fileAPI, *fakeCrypto, *line.Message)
	}{
		{"missing oid", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { delete(m.ContentMetadata, "OID") }},
		{"wrong sid", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.ContentMetadata["SID"] = "emv" }},
		{"plain sid", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.ContentMetadata["SID"] = "m" }},
		{"missing chunks", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.Chunks = nil }},
		{"bad chunks", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.Chunks = []string{"bad"} }},
		{"bad sender key", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.Chunks[3] = "invalid" }},
		{"missing own keys", func(c *Client, _ *fileAPI, _ *fakeCrypto, _ *line.Message) { c.crypto = nil }},
		{"mismatched peer", func(_ *Client, a *fileAPI, _ *fakeCrypto, _ *line.Message) { a.key.KeyID = "99" }},
		{"bad payload", func(_ *Client, _ *fileAPI, c *fakeCrypto, _ *line.Message) { c.payload = "bad json" }},
		{"missing material", func(_ *Client, _ *fileAPI, c *fakeCrypto, _ *line.Message) { c.payload = `{}` }},
		{"bad material", func(_ *Client, _ *fileAPI, c *fakeCrypto, _ *line.Message) { c.payload = `{"keyMaterial":"%%%"}` }},
		{"short material", func(_ *Client, _ *fileAPI, c *fakeCrypto, _ *line.Message) { c.payload = `{"keyMaterial":"YQ=="}` }},
		{"conflicting material", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.ContentMetadata["ENC_KM"] = "untrusted" }},
		{"decryption failure", func(_ *Client, _ *fileAPI, c *fakeCrypto, _ *line.Message) {
			c.decryptErr = errors.New("private server body")
		}},
		{"unsupported", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.ContentType = 7 }},
		{"external URL", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) {
			m.ContentMetadata["DOWNLOAD_URL"] = "https://example.invalid/private"
		}},
		{"advertised oversized", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) {
			m.ContentMetadata["FILE_SIZE"] = "20971521"
		}},
		{"advertised negative", func(_ *Client, _ *fileAPI, _ *fakeCrypto, m *line.Message) { m.ContentMetadata["FILE_SIZE"] = "-1" }},
		{"not in history", func(_ *Client, a *fileAPI, _ *fakeCrypto, _ *line.Message) { a.history = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, api, crypto, msg := mediaFixture(t, 1, "u-peer", true)
			tc.mutate(c, api, crypto, msg)
			data, err := c.DownloadFile(context.Background(), "u-peer", "123")
			if err == nil || data != nil || api.downloads != 0 || api.registrations != 0 || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe failure: %v; downloads=%d", err, api.downloads)
			}
		})
	}
}

func TestMediaDownloadPlainMetadataAndMissingGroupKey(t *testing.T) {
	for _, metadata := range []map[string]string{{"e2eeVersion": "2"}, {"ENC_KM": "untrusted"}, {"SID": "emi"}, {"SID": "unexpected"}} {
		c, api, _, msg := mediaFixture(t, 1, "u-peer", false)
		msg.ContentMetadata = metadata
		if _, err := c.DownloadFile(context.Background(), "u-peer", "123"); err == nil || api.downloads != 0 {
			t.Fatal("encryption indication fell back to plaintext")
		}
	}
	c, api, _, _ := mediaFixture(t, 2, "c-group", true)
	api.groupErr = line.ErrGroupKeyNotFound
	if _, err := c.DownloadFile(context.Background(), "c-group", "123"); err == nil || api.registrations != 0 || api.downloads != 0 {
		t.Fatal("missing group key must not be registered")
	}
}

func TestMediaDownloadIntegrityAndSizeLimits(t *testing.T) {
	for _, kind := range []int{1, 2, 3, 14} {
		for _, tamper := range []string{"body", "tag", "truncated"} {
			t.Run(fmt.Sprintf("%d/%s", kind, tamper), func(t *testing.T) {
				c, api, _, _ := mediaFixture(t, kind, "u-peer", true)
				switch tamper {
				case "body":
					api.data[0] ^= 1
				case "tag":
					api.data[len(api.data)-1] ^= 1
				case "truncated":
					api.data = api.data[:10]
				}
				if data, err := c.DownloadFile(context.Background(), "u-peer", "123"); err == nil || data != nil || api.downloads != 1 {
					t.Fatal("unauthenticated data returned or retried")
				}
			})
		}
	}
	for _, size := range []int{MaxAttachmentBytes, MaxAttachmentBytes + 1, MaxAttachmentBytes + 33} {
		for _, advertised := range []string{"", "1", "invalid"} {
			c, api, _, msg := mediaFixture(t, 3, "u-peer", false)
			api.data = make([]byte, size)
			msg.ContentMetadata["FILE_SIZE"] = advertised
			data, err := c.DownloadFile(context.Background(), "u-peer", "123")
			if size == MaxAttachmentBytes && (err != nil || len(data) != size) {
				t.Fatal("exact limit failed", err)
			}
			if size > MaxAttachmentBytes && (err == nil || data != nil) {
				t.Fatal("actual size limit bypassed")
			}
		}
	}
	c, api, _, _ := mediaFixture(t, 1, "u-peer", true)
	var key string
	var err error
	api.data, key, err = encryptFile(make([]byte, MaxAttachmentBytes))
	if err != nil {
		t.Fatal(err)
	}
	c.crypto.(*fakeCrypto).payload = `{"keyMaterial":"` + key + `"}`
	if data, err := c.DownloadFile(context.Background(), "u-peer", "123"); err != nil || len(data) != MaxAttachmentBytes {
		t.Fatal("encrypted exact limit failed", err)
	}
}

func TestMediaDownloadSafeTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{line.ErrOBSObjectNotFound, "no longer available"},
		{line.ErrOBSEncodingIncomplete, "still processing"},
		{line.ErrOBSSizeLimit, "20 MiB"},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline exceeded"},
		{errors.New("OBS download failed (401): private body"), "requires authentication"},
		{errors.New("private raw server body"), "check your connection"},
	} {
		c, api, _, _ := mediaFixture(t, 1, "u-peer", false)
		api.downloadErr = tc.err
		data, err := c.DownloadFile(context.Background(), "u-peer", "123")
		if err == nil || data != nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe transport error: %v", err)
		}
	}
	c, api, _, _ := mediaFixture(t, 1, "u-peer", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.DownloadFile(ctx, "u-peer", "123"); !errors.Is(err, context.Canceled) || api.downloads != 0 {
		t.Fatal("cancelled download continued")
	}
}

// Independent vectors generated with Node WebCrypto, using Chrome's HKDF,
// SHA-256 per 131072 bytes, HMAC, and 32-bit AES-CTR. Ciphertext byte i is i%251;
// key material is bytes 0..31. Plaintext is checked by its SHA-256 digest.
func TestVideoDecryptionWebCryptoVectors(t *testing.T) {
	material := make([]byte, 32)
	for i := range material {
		material[i] = byte(i)
	}
	key := base64.StdEncoding.EncodeToString(material)
	for _, tc := range []struct {
		size          int
		tag, plainSHA string
	}{
		{0, "632aaa527a64f6c04fc88d463f2b699a65eec9b32559b310f27b7fbfd24e29e8", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{1, "8cfddb123563c2863543b8e07ff8c62c8e9a03871fd37f4591e3b5cf25f9f621", "a25513c7e0f6eaa80a3337ee18081b9e2ed09e00af8531c8f7bb2542764027e7"},
		{131071, "13bba3cb9f7f68642c3e91b9bab91fc1ca472ad948ba0452b2b3d81a30874258", "ec6d1ee11b28d1c191b2d08ea46e0b0bcb00e142fa6c3857237fe7352e1f4314"},
		{131072, "944da0d9fa5272dfbff9240ffad9a835df392942f405d6950dd8f4e11da45d96", "41c9cdfa6cfb5e6b2ca0c4c4aa8ae15fed2d699c974b1ec9bb1932ddda8cea9b"},
		{131073, "3b3871014449485c3ec91a467fa48177db03c051cc6fbbe6d3f7b7d03e5da76d", "774213bad95dcced56d58082bb7dbceca40244b7ae34fbdb92c8fc9401f418e6"},
		{262145, "6bf09585cd5b4401e0300e0a4701a7ac2ad7ff5d47bd4caaf64d929bebe7ef3e", "5597e446d7ae3d2fe94520762605c4db65a92570e20d6dfcde940cc078745a00"},
	} {
		t.Run(fmt.Sprint(tc.size), func(t *testing.T) {
			body := make([]byte, tc.size)
			for i := range body {
				body[i] = byte(i % 251)
			}
			tag, _ := hex.DecodeString(tc.tag)
			data := append(body, tag...)
			plain, err := decryptAttachment(data, key, true)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(plain)
			if hex.EncodeToString(digest[:]) != tc.plainSHA {
				t.Fatal("WebCrypto plaintext mismatch")
			}
			if tc.size > 0 {
				if _, err := decryptAttachment(data, key, false); err == nil {
					t.Fatal("video incorrectly authenticated with ordinary MAC")
				}
			}
		})
	}
}

func intPtr(n int) *int { return &n }

func albumPreviewFixture(t *testing.T, chat string, msg *line.Message) (*Client, *fileAPI) {
	t.Helper()
	c, f, _, _ := setup(t, true)
	api := &fileAPI{fakeAPI: f, albumData: []byte("jpeg-thumb")}
	c.Session.NewClient = func(string) session.API { return api }
	if msg.From == "" {
		msg.From = "u-bob"
	}
	if toType(chat) != 0 {
		msg.To = chat
		msg.ToType = toType(chat)
	} else {
		msg.From = chat
		msg.To = "u-self"
		msg.ToType = 0
	}
	f.history = []*line.Message{msg}
	return c, api
}

func TestAlbumPreviewDownloadSelectionAndValidation(t *testing.T) {
	albumMsg := &line.Message{
		ID: "5002", From: "u-bob", To: "c1234", ToType: 2, ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType": "AB",
			"albumName":   "Summit photos",
			"postEndUrl":  "line://group/home/albums/album?albumId=123&albumIdV2=abc-v2",
			"chatId":      "c1234",
			"previewMedias": `[
				{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},
				"malformed-entry",
				{"svc":"album","sid":"a","mediaOid":"../bad","mediaType":"I"},
				{"svc":"album","sid":"a","mediaOid":"oid-2","mediaType":"I"}
			]`,
		},
	}

	c, api := albumPreviewFixture(t, "c1234", albumMsg)
	decoded := c.Decode("c1234", albumMsg)
	if decoded.Content.Post == nil || decoded.Content.Post.PreviewCount == nil || *decoded.Content.Post.PreviewCount != 2 {
		t.Fatalf("unexpected decoded preview_count: %+v", decoded.Content)
	}

	// Default omitted preview downloads index 1
	data, err := c.DownloadFileWithOptions(context.Background(), "c1234", "5002", DownloadOptions{})
	if err != nil || string(data) != "jpeg-thumb" || api.albumOID != "oid-1" || api.albumChatID != "c1234" || api.albumID != "abc-v2" {
		t.Fatalf("index 1 download: data=%q err=%v oid=%q chat=%q album=%q", data, err, api.albumOID, api.albumChatID, api.albumID)
	}

	// Explicit index 2 matches the second retained valid sibling
	data, err = c.DownloadFileWithOptions(context.Background(), "c1234", "5002", DownloadOptions{Preview: intPtr(2)})
	if err != nil || string(data) != "jpeg-thumb" || api.albumOID != "oid-2" || api.albumChatID != "c1234" || api.albumID != "abc-v2" {
		t.Fatalf("index 2 download: data=%q err=%v oid=%q chat=%q album=%q", data, err, api.albumOID, api.albumChatID, api.albumID)
	}

	// Out-of-range index 3 fails with invalid_arguments and makes no OBS call
	before := api.albumPreviewCalls
	_, err = c.DownloadFileWithOptions(context.Background(), "c1234", "5002", DownloadOptions{Preview: intPtr(3)})
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Code != "invalid_arguments" || !strings.Contains(fe.Message, "between 1 and 2") || api.albumPreviewCalls != before {
		t.Fatalf("out-of-range err = %v, calls = %d", err, api.albumPreviewCalls-before)
	}

	// Plaintext type-0 ORGCONTP Album wrapper also downloads cleanly
	wrapAlbum := &line.Message{
		ID: "5003", From: "u-bob", To: "c1234", ToType: 2, ContentType: 0, Text: "Bob added photos",
		ContentMetadata: map[string]string{
			"ORGCONTP":      "POSTNOTIFICATION",
			"serviceType":   "AB",
			"albumName":     "Wrapped",
			"chatId":        "c1234",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-w1","mediaType":"I"}]`,
		},
	}
	c2, api2 := albumPreviewFixture(t, "c1234", wrapAlbum)
	if _, err := c2.DownloadFile(context.Background(), "c1234", "5003"); err != nil || api2.albumOID != "oid-w1" || api2.albumChatID != "c1234" || api2.albumID != "" {
		t.Fatalf("wrapper album download failed: %v oid=%q", err, api2.albumOID)
	}
}

func TestAlbumPreviewRejectionsMakeZeroOBSCalls(t *testing.T) {
	cases := []struct {
		name     string
		chat     string
		msg      *line.Message
		preview  *int
		wantCode string
	}{
		{
			name: "cross-chat album",
			chat: "c-selected",
			msg: &line.Message{
				ID: "1", ContentType: 16,
				ContentMetadata: map[string]string{
					"serviceType":   "AB",
					"chatId":        "c-other",
					"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
				},
			},
			wantCode: "unsupported_content",
		},
		{
			name: "duplicate homeId on exact chatId match",
			chat: "c1234",
			msg: &line.Message{
				ID: "1", ContentType: 16,
				ContentMetadata: map[string]string{
					"serviceType":   "AB",
					"chatId":        "c1234",
					"postEndUrl":    "line://group/home/albums/album?albumId=123&homeId=c1234&homeId=c-other",
					"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
				},
			},
			wantCode: "unsupported_content",
		},
		{
			name: "note post",
			chat: "c1234",
			msg: &line.Message{
				ID: "1", ContentType: 16,
				ContentMetadata: map[string]string{"serviceType": "GB", "text": "hello"},
			},
			wantCode: "unsupported_content",
		},
		{
			name: "album with 0 previews",
			chat: "c1234",
			msg: &line.Message{
				ID: "1", ContentType: 16,
				ContentMetadata: map[string]string{"serviceType": "AB", "chatId": "c1234", "previewMedias": `[]`},
			},
			wantCode: "unsupported_content",
		},
		{
			name: "encrypted native 16",
			chat: "c1234",
			msg: &line.Message{
				ID: "1", ContentType: 16, Chunks: encryptedChunks(22, 11),
				ContentMetadata: map[string]string{
					"serviceType":   "AB",
					"chatId":        "c1234",
					"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
				},
			},
			wantCode: "unsupported_content",
		},
		{
			name: "encrypted type 0 ORGCONTP",
			chat: "c1234",
			msg: &line.Message{
				ID: "1", ContentType: 0, Chunks: encryptedChunks(22, 11),
				ContentMetadata: map[string]string{
					"ORGCONTP":      "POSTNOTIFICATION",
					"serviceType":   "AB",
					"chatId":        "c1234",
					"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
				},
			},
			wantCode: "unsupported_content",
		},
		{
			name:     "explicit --preview on ordinary image",
			chat:     "u-peer",
			msg:      &line.Message{ID: "1", ContentType: 1},
			preview:  intPtr(1),
			wantCode: "invalid_arguments",
		},
		{
			name: "explicit --preview on type 14 attachment with ORGCONTP",
			chat: "u-peer",
			msg: &line.Message{
				ID: "1", ContentType: 14,
				ContentMetadata: map[string]string{"ORGCONTP": "POSTNOTIFICATION", "serviceType": "AB", "chatId": "u-peer"},
			},
			preview:  intPtr(1),
			wantCode: "invalid_arguments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, api := albumPreviewFixture(t, tc.chat, tc.msg)
			data, err := c.DownloadFileWithOptions(context.Background(), tc.chat, "1", DownloadOptions{Preview: tc.preview})
			var fe *fault.Error
			if err == nil || data != nil || !errors.As(err, &fe) || fe.Code != tc.wantCode {
				t.Fatalf("err = %v, data = %v, want code %s", err, data, tc.wantCode)
			}
			if api.albumPreviewCalls != 0 || api.downloads != 0 {
				t.Fatalf("unexpected download calls: album=%d obs=%d", api.albumPreviewCalls, api.downloads)
			}
		})
	}
}

func TestAlbumPreviewSizeLimitReturnsNoData(t *testing.T) {
	msg := &line.Message{
		ID: "5002", ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"chatId":        "c1234",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	c, api := albumPreviewFixture(t, "c1234", msg)
	api.albumData = nil
	api.albumErr = line.ErrOBSSizeLimit
	data, err := c.DownloadFileWithOptions(context.Background(), "c1234", "5002", DownloadOptions{})
	if err == nil || data != nil || !strings.Contains(err.Error(), "20 MiB") {
		t.Fatalf("oversized error = %v, data = %v", err, data)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAlbumPreview403OversizedBodyRecoversViaRefresh(t *testing.T) {
	albumMsg := &line.Message{
		ID: "5002", From: "u-bob", To: "c1234", ToType: 2, ContentType: 16,
		ContentMetadata: map[string]string{
			"serviceType":   "AB",
			"chatId":        "c1234",
			"postEndUrl":    "line://group/home/albums/album?albumIdV2=v2-1",
			"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`,
		},
	}
	historyJSON, _ := json.Marshal(map[string]any{"code": 0, "message": "OK", "data": []*line.Message{albumMsg}})
	large403 := "private-html-" + strings.Repeat("x", 8192)

	var channelTokens, obsCalls, refreshes int
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/TalkService/getRecentMessagesV2"):
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(historyJSON)))}, nil
		case strings.HasSuffix(req.URL.Path, "/ChannelService/issueChannelToken"):
			channelTokens++
			tok := fmt.Sprintf("ch-tok-%d", channelTokens)
			body := fmt.Sprintf(`{"code":0,"message":"OK","data":{"channelAccessToken":%q,"expiration":9999999999999}}`, tok)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case strings.HasSuffix(req.URL.Path, "/api/auth/tokenRefresh"):
			refreshes++
			body := `{"accessToken":"refreshed-token","refreshToken":"new-refresh","durationUntilRefreshInSec":"3600"}`
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		case req.URL.Host == "obs.line-apps.com" && req.URL.Path == "/r/album/a/oid-1/f482x482":
			obsCalls++
			if req.Header.Get("X-Line-Access") == "stale-token" {
				return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(large403))}, nil
			}
			if req.Header.Get("X-Line-ChannelToken") != "ch-tok-2" || req.Header.Get("X-Line-Album") != "v2-1" || req.Header.Get("X-Line-Mid") != "c1234" {
				return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("wrong headers"))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("recovered-jpeg"))}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unexpected " + req.URL.String()))}, nil
		}
	})

	store := &memStore{state: &session.State{
		Version:      1,
		Generation:   "gen-1",
		MID:          "u-self",
		AccessToken:  "stale-token",
		RefreshToken: "refresh-1",
		NoE2EE:       true,
	}}
	mgr := session.NewManager(store)
	mgr.NewClient = func(token string) session.API {
		cl := line.NewClient(token)
		cl.HTTPClient = &http.Client{Transport: transport}
		cl.OBSClient = &http.Client{Transport: transport}
		return cl
	}
	client, err := newClient(mgr, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.DownloadFileWithOptions(context.Background(), "c1234", "5002", DownloadOptions{})
	if err != nil || string(data) != "recovered-jpeg" {
		t.Fatalf("recovery failed: data=%q err=%v", data, err)
	}
	if refreshes != 1 || channelTokens != 2 || obsCalls != 2 {
		t.Fatalf("counts: refreshes=%d channelTokens=%d obsCalls=%d", refreshes, channelTokens, obsCalls)
	}
}
