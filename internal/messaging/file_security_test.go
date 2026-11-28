package messaging

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	gen "github.com/kongesque/line-cli/pkg"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
	"github.com/kongesque/line-cli/pkg/ltsm"
)

type claimTransport func(*http.Request) (*http.Response, error)

func (f claimTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func claimResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

type claimFileAPI struct {
	*fileAPI
	wire      *line.Client
	refreshes int
}

func (a *claimFileAPI) SendMessageContext(ctx context.Context, seq int64, m *line.Message) (*line.Message, error) {
	return a.wire.SendMessageContext(ctx, seq, m)
}

func (a *claimFileAPI) DownloadOBSPublicResourceWithOptions(ctx context.Context, resourceURL string, opts line.OBSDownloadOptions) ([]byte, error) {
	return a.wire.DownloadOBSPublicResourceWithOptions(ctx, resourceURL, opts)
}

func (a *claimFileAPI) RefreshAccessTokenContext(context.Context, string) (*line.TokenV3IssueResult, error) {
	a.refreshes++
	return &line.TokenV3IssueResult{AccessToken: "refreshed-token", RefreshToken: "rotated-refresh"}, nil
}

func TestFileWireEnvelopeProtectsKeyAndName(t *testing.T) {
	for _, chat := range []string{"u-peer", "c-group", "r-room"} {
		t.Run(chat, func(t *testing.T) {
			c, f, crypto, _ := setup(t, false)
			f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "synthetic"}
			files := &fileAPI{fakeAPI: f}
			wire := line.NewClient("synthetic-token")
			var outer line.Message
			requests := 0
			wire.HTTPClient.Transport = claimTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
					t.Fatal("unexpected API request")
				}
				requests++
				var args []json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args) != 2 {
					t.Fatal("invalid send argument envelope")
				}
				if err := json.Unmarshal(args[1], &outer); err != nil {
					t.Fatal("invalid serialized message")
				}
				return claimResponse(r, 200, `{"code":0,"data":{"id":"synthetic-receipt"}}`), nil
			})
			api := &claimFileAPI{fileAPI: files, wire: wire}
			crypt := &fileCrypto{fakeCrypto: crypto}
			c.crypto = crypt
			c.Session.NewClient = func(string) session.API { return api }
			file := Attachment{Name: "synthetic-private.txt", Data: []byte("synthetic confidential attachment")}
			f.history = []*line.Message{{ID: "123", From: "u-self", To: chat, ToType: toType(chat)}}
			result, err := c.SendFile(chat, file, "123")
			if err != nil || result == nil || !result.Encrypted || requests != 1 || len(outer.Chunks) != 5 {
				t.Fatal("encrypted file send did not complete through real HTTP serialization")
			}
			if outer.ContentMetadata["ENC_KM"] != "" || outer.ContentMetadata["FILE_NAME"] != "" || outer.RelatedMessageID != "123" {
				t.Fatal("outer envelope exposed attachment secrets or lost reply relation")
			}
			if _, err := decryptFile(files.uploaded, outer.ContentMetadata["ENC_KM"]); err == nil {
				t.Fatal("metadata-only decryption succeeded")
			}
			var payload map[string]string
			if json.Unmarshal(crypt.payloadSent, &payload) != nil || payload["fileName"] != file.Name {
				t.Fatal("authenticated payload lost filename")
			}
			plain, err := decryptFile(files.uploaded, payload["keyMaterial"])
			if err != nil || !bytes.Equal(plain, file.Data) {
				t.Fatal("recipient payload did not decrypt attachment")
			}
			wireBody, _ := json.Marshal(outer)
			receipt, _ := json.Marshal(result)
			for _, secret := range []string{payload["keyMaterial"], file.Name, string(file.Data)} {
				if bytes.Contains(wireBody, []byte(secret)) || bytes.Contains(receipt, []byte(secret)) {
					t.Fatal("wire envelope or receipt exposed attachment secret")
				}
			}
		})
	}
}

// Keep the envelope probe above independent of Letter Sealing; this separate
// roundtrip exercises the real manager/runner and recipient crypto as well.
type realFileCrypto struct {
	*fakeCrypto
	manager   *e2ee.Manager
	senderKey int
}

func (r *realFileCrypto) MyKeyIDs() (int, int, error) { return 11, r.senderKey, nil }
func (r *realFileCrypto) EncryptMessageV2Raw(chat, from string, own int, pub string, sender, receiver, kind int, payload []byte) ([]string, error) {
	return r.manager.EncryptMessageV2Raw(chat, from, own, pub, sender, receiver, kind, payload)
}
func TestEncryptedFileRealRecipientRoundTrip(t *testing.T) {
	runner, err := gen.GetRunner()
	if err != nil {
		t.Fatal("runner initialization failed")
	}
	senderKey, err := runner.KeyGenerate()
	if err != nil {
		t.Fatal("synthetic sender key failed")
	}
	public, err := runner.KeyGetPublic(senderKey)
	if err != nil {
		t.Fatal("synthetic sender public key failed")
	}
	receiver, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("synthetic receiver key failed")
	}
	manager, err := e2ee.NewManager()
	if err != nil {
		t.Fatal("manager initialization failed")
	}
	manager.RegisterUnwrappedKey(11, senderKey)
	c, f, crypto, _ := setup(t, false)
	c.state.MID = "u11111111111111111111111111111111"
	const chat = "u22222222222222222222222222222222"
	f.key.PublicKey = base64.StdEncoding.EncodeToString(receiver.PublicKey().Bytes())
	c.crypto = &realFileCrypto{fakeCrypto: crypto, manager: manager, senderKey: senderKey}
	api := &fileAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return api }
	attachment := Attachment{Name: "synthetic-private.txt", Data: []byte("synthetic confidential bytes")}
	result, err := c.SendFile(chat, attachment, "")
	if err != nil || result == nil || !result.Encrypted || len(f.sent.Chunks) != 5 {
		t.Fatalf("real encrypted file send failed: %v", err)
	}
	if f.sent.ContentMetadata["ENC_KM"] != "" || f.sent.ContentMetadata["FILE_NAME"] != "" {
		t.Fatal("secrets outside sealed payload")
	}
	senderPublic, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal("invalid synthetic public key")
	}
	recipient, err := ltsm.NewChannel(receiver.Bytes(), senderPublic)
	if err != nil {
		t.Fatal("recipient channel failed")
	}
	parts := make([][]byte, 3)
	for i := range parts {
		parts[i], err = base64.StdEncoding.DecodeString(f.sent.Chunks[i])
		if err != nil {
			t.Fatal("invalid chunk encoding")
		}
	}
	// The generated-key runner can use the manager's existing V1 compatibility
	// path. Decode the actual wire format with the production recipient crypto.
	order := []int{0, 2, 1}
	decrypt := func(cipher []byte) ([]byte, error) {
		return recipient.DecryptV2(chat, c.state.MID, 11, 22, 14, cipher)
	}
	if len(parts[0]) == 8 {
		order = []int{0, 1, 2}
		decrypt = recipient.DecryptV1
	}
	var cipher []byte
	for _, i := range order {
		cipher = append(cipher, parts[i]...)
	}
	payload, err := decrypt(cipher)
	if err != nil {
		t.Fatal("recipient could not authenticate sealed payload")
	}
	var decoded map[string]string
	if json.Unmarshal(payload, &decoded) != nil || decoded["fileName"] != attachment.Name {
		t.Fatal("recipient payload lost filename")
	}
	plain, err := decryptFile(api.uploaded, decoded["keyMaterial"])
	if err != nil || !bytes.Equal(plain, attachment.Data) {
		t.Fatal("recipient could not authenticate attachment")
	}
	cipher[len(cipher)-1] ^= 1
	if _, err := decrypt(cipher); err == nil {
		t.Fatal("tampered sealed payload accepted")
	}
}

func TestDownloadExternalURLSafetyAndNoTokenLeak(t *testing.T) {
	c, f, _, store := setup(t, false)
	store.state.RefreshToken = "synthetic-refresh-token"
	wire := line.NewClient("super-secret-line-token")
	var seenReq *http.Request
	wire.PublicMediaClient = &http.Client{
		Transport: claimTransport(func(r *http.Request) (*http.Response, error) {
			seenReq = r
			if r.URL.Path == "/oversized" {
				return claimResponse(r, http.StatusOK, strings.Repeat("a", MaxAttachmentBytes+1)), nil
			}
			return claimResponse(r, http.StatusOK, "official-account-image"), nil
		}),
	}
	api := &claimFileAPI{fileAPI: &fileAPI{fakeAPI: f}, wire: wire}
	c.Session.NewClient = func(string) session.API { return api }

	// 1. Plain external DOWNLOAD_URL succeeds without sending LINE auth headers.
	f.history = []*line.Message{{
		ID:          "901",
		From:        "u-official",
		To:          "u-self",
		ContentType: 1,
		ContentMetadata: map[string]string{
			"DOWNLOAD_URL": "https://cdn.example.com/official/photo.jpg",
		},
	}}
	data, err := c.DownloadFile(context.Background(), "u-official", "901")
	if err != nil || string(data) != "official-account-image" {
		t.Fatalf("external DOWNLOAD_URL failed: data=%q err=%v", data, err)
	}
	if seenReq == nil {
		t.Fatal("external DOWNLOAD_URL did not hit PublicMediaClient")
	}
	for _, forbidden := range []string{"X-Line-Access", "Cookie", "X-Line-Application", "X-Talk-Meta"} {
		if seenReq.Header.Get(forbidden) != "" {
			t.Fatalf("external host received forbidden header %s", forbidden)
		}
	}

	// 2. Oversized external response fails with the 20 MiB limit.
	f.history[0].ContentMetadata["DOWNLOAD_URL"] = "https://cdn.example.com/oversized"
	if data, err := c.DownloadFile(context.Background(), "u-official", "901"); err == nil || data != nil || !strings.Contains(err.Error(), "20 MiB") {
		t.Fatalf("oversized external DOWNLOAD_URL not bounded: err=%v len=%d", err, len(data))
	}

	// 3. Private IP external URL is blocked without leaking the URL.
	seenReq = nil
	f.history[0].ContentMetadata["DOWNLOAD_URL"] = "http://127.0.0.1/private-secret-path"
	if data, err := c.DownloadFile(context.Background(), "u-official", "901"); err == nil || data != nil || seenReq != nil || strings.Contains(err.Error(), "private-secret-path") {
		t.Fatalf("private IP DOWNLOAD_URL not blocked safely: err=%v req=%v", err, seenReq)
	}

	// 4. Relative OBS path DOWNLOAD_URL uses OBS client without auth headers.
	var obsReq *http.Request
	wire.OBSClient = &http.Client{
		Transport: claimTransport(func(r *http.Request) (*http.Response, error) {
			obsReq = r
			return claimResponse(r, http.StatusOK, "obs-public-bytes"), nil
		}),
	}
	f.history[0].ContentMetadata["DOWNLOAD_URL"] = "/r/official/item-1"
	data, err = c.DownloadFile(context.Background(), "u-official", "901")
	if err != nil || string(data) != "obs-public-bytes" || obsReq == nil || obsReq.Header.Get("X-Line-Access") != "" {
		t.Fatalf("relative OBS DOWNLOAD_URL failed: data=%q err=%v req=%v", data, err, obsReq)
	}

	// 5. External 401/403 must never trigger LINE token refresh or classify as LINE auth error.
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		externalHits := 0
		wire.PublicMediaClient = &http.Client{
			Transport: claimTransport(func(r *http.Request) (*http.Response, error) {
				externalHits++
				return claimResponse(r, status, "external-secret-error"), nil
			}),
		}
		api.refreshes = 0
		f.history[0].ContentMetadata["DOWNLOAD_URL"] = "https://cdn.example.com/expired-signed-url"
		data, err = c.DownloadFile(context.Background(), "u-official", "901")
		if err == nil || data != nil {
			t.Fatalf("expected error for external status %d, got data=%q err=%v", status, data, err)
		}
		if externalHits != 1 || api.refreshes != 0 {
			t.Fatalf("external %d triggered retry or token refresh: hits=%d refreshes=%d", status, externalHits, api.refreshes)
		}
		if line.IsAuthError(session.ProtocolError(err)) || strings.Contains(err.Error(), "external-secret-error") {
			t.Fatalf("external %d misclassified as LINE auth error or leaked body: %v", status, err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("status %d", status)) {
			t.Fatalf("external %d missing status message: %v", status, err)
		}
	}
}
