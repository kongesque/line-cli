package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type claimTransport func(*http.Request) (*http.Response, error)

func (f claimTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func claimResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

type claimFileAPI struct {
	*fileAPI
	wire *line.Client
}

func (a *claimFileAPI) SendMessageContext(ctx context.Context, seq int64, m *line.Message) (*line.Message, error) {
	return a.wire.SendMessageContext(ctx, seq, m)
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
