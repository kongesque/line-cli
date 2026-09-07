package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type fileAPI struct {
	*fakeAPI
	order                            []string
	uploaded                         []byte
	data                             []byte
	options                          line.OBSDownloadOptions
	requestSID, requestOID, talkMeta string
	uploadErr, plainErr              error
}

func (f *fileAPI) UploadOBSWithSID(data []byte, sid string) (string, error) {
	f.order = append(f.order, "upload")
	f.uploaded = append([]byte(nil), data...)
	if sid != "emf" {
		panic("wrong file SID")
	}
	return "object-id", f.uploadErr
}
func (f *fileAPI) UploadOBSPlain(data []byte, oid, kind string) error {
	f.order = append(f.order, "plain_upload")
	f.uploaded = append([]byte(nil), data...)
	if oid != "server-id" || kind != "file" {
		panic("wrong post-send upload target")
	}
	return f.plainErr
}
func (f *fileAPI) SendMessage(seq int64, msg *line.Message) (*line.Message, error) {
	f.order = append(f.order, "send")
	return f.fakeAPI.SendMessage(seq, msg)
}
func (f *fileAPI) DownloadOBSWithSIDOptions(_ context.Context, oid, message, sid string, opts line.OBSDownloadOptions) ([]byte, error) {
	f.options = opts
	f.requestOID, f.talkMeta, f.requestSID = oid, message, sid
	return f.data, nil
}

type fileCrypto struct {
	*fakeCrypto
	payloadSent []byte
	contentType int
}

func (f *fileCrypto) EncryptMessageV2Raw(_ string, _ string, _ int, _ string, _ int, _ int, kind int, payload []byte) ([]string, error) {
	f.payloadSent = payload
	f.contentType = kind
	return encryptedChunks(11, 22), f.encryptErr
}
func (f *fileCrypto) EncryptGroupMessageRaw(_ string, _ string, kind int, payload []byte) ([]string, error) {
	f.payloadSent = payload
	f.contentType = kind
	return encryptedChunks(11, 33), f.encryptErr
}

func TestFileSendEnvelopeAndUploadOrdering(t *testing.T) {
	for _, chat := range []string{"u-peer", "c-group"} {
		for _, plain := range []bool{false, true} {
			c, f, crypto, _ := setup(t, plain)
			api := &fileAPI{fakeAPI: f}
			crypt := &fileCrypto{fakeCrypto: crypto}
			c.crypto = crypt
			c.Session.NewClient = func(string) session.API { return api }
			f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
			attachment := Attachment{Name: "notes.pdf", Data: []byte("private attachment bytes")}
			result, err := c.SendFile(chat, attachment, "123")
			if err != nil {
				t.Fatal(err)
			}
			if result.Encrypted == plain || f.sent.ContentType != 14 || !f.sent.HasContent || f.sent.ContentMetadata["FILE_NAME"] != "notes.pdf" || f.sent.RelatedMessageID != "123" {
				t.Fatal("incorrect attachment envelope")
			}
			if plain {
				if strings.Join(api.order, ",") != "send,plain_upload" || !bytes.Equal(api.uploaded, attachment.Data) {
					t.Fatal("plain upload must follow message creation")
				}
			} else {
				if strings.Join(api.order, ",") != "upload,send" || bytes.Contains(api.uploaded, attachment.Data) || crypt.contentType != 14 {
					t.Fatal("encrypted upload order/body incorrect")
				}
				var payload map[string]string
				if err := json.Unmarshal(crypt.payloadSent, &payload); err != nil {
					t.Fatal(err)
				}
				data, err := decryptFile(api.uploaded, payload["keyMaterial"])
				if err != nil || !bytes.Equal(data, attachment.Data) {
					t.Fatal("uploaded file does not decrypt", err)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), payload["keyMaterial"]) {
					t.Fatal("file key exposed in send result")
				}
			}
		}
	}
}

func TestFileUploadFailureNeverRetriesOrSendsEmptyEncryptedMessage(t *testing.T) {
	for _, plain := range []bool{false, true} {
		c, f, crypto, _ := setup(t, plain)
		api := &fileAPI{fakeAPI: f, uploadErr: errors.New("private body"), plainErr: errors.New("private body")}
		c.crypto = &fileCrypto{fakeCrypto: crypto}
		c.Session.NewClient = func(string) session.API { return api }
		_, err := c.SendFile("u-peer", Attachment{Name: "test.bin", Data: []byte("test")}, "")
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe upload error", err)
		}
		if (!plain && f.sends != 0) || (plain && (f.sends != 1 || !strings.Contains(err.Error(), "server-id"))) {
			t.Fatal("incorrect mutation/retry after upload error")
		}
	}
}

func TestFileDownloadAuthenticatesAndUsesCorrectOBSContract(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		c, f, crypto, _ := setup(t, false)
		api := &fileAPI{fakeAPI: f, data: []byte("file data")}
		c.Session.NewClient = func(string) session.API { return api }
		msg := &line.Message{ID: "123", From: "u-peer", To: "u-self", ContentType: 14, ContentMetadata: map[string]string{"FILE_SIZE": "9"}}
		f.history = []*line.Message{msg}
		if encrypted {
			var key string
			var err error
			api.data, key, err = encryptFile(api.data)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]string{"keyMaterial": key})
			crypto.payload = string(payload)
			msg.Chunks = encryptedChunks(22, 11)
			msg.ContentMetadata["OID"] = "object-id"
		}
		data, err := c.DownloadFile(context.Background(), "u-peer", "123")
		if err != nil || string(data) != "file data" {
			t.Fatal("download failed", err)
		}
		if api.options.MaxBytes != MaxAttachmentBytes+32 {
			t.Fatal("download was not bounded")
		}
		if encrypted {
			if api.requestSID != "emf" || api.talkMeta != "123" || api.requestOID != "object-id" {
				t.Fatal("bad encrypted OBS request")
			}
			api.data[0] ^= 1
			if _, err := c.DownloadFile(context.Background(), "u-peer", "123"); err == nil {
				t.Fatal("tampered file accepted")
			}
		} else if api.requestSID != "m" || api.talkMeta != "" || api.requestOID != "123" {
			t.Fatal("bad plain OBS request")
		}
	}
}

func TestAttachmentValidationAndMAC(t *testing.T) {
	for _, name := range []string{"", "../file", "dir\\file", "file\nname"} {
		if (Attachment{Name: name}).Validate() == nil {
			t.Fatal("unsafe filename accepted")
		}
	}
	if (Attachment{Name: "large", Data: make([]byte, MaxAttachmentBytes+1)}).Validate() == nil {
		t.Fatal("oversized attachment accepted")
	}
	data, key, err := encryptFile(nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := decryptFile(data, key); err != nil || len(out) != 0 {
		t.Fatal("empty file failed", err)
	}
	data[len(data)-1] ^= 1
	if _, err := decryptFile(data, key); err == nil {
		t.Fatal("invalid MAC accepted")
	}
	if _, err := decryptFile([]byte("short"), key); err == nil {
		t.Fatal("truncated file accepted")
	}
}
