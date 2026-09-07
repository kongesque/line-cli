package messaging

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/hkdf"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const MaxAttachmentBytes = 20 << 20

type Attachment struct {
	Name string
	Data []byte
}

func (f Attachment) Validate() error {
	if f.Name == "" || len(f.Name) > 255 || !utf8.ValidString(f.Name) || strings.ContainsAny(f.Name, "/\\") || strings.ContainsFunc(f.Name, unicode.IsControl) {
		return errors.New("attachment needs a valid file name without path separators or control characters")
	}
	if len(f.Data) > MaxAttachmentBytes {
		return errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	return nil
}

// The existing bridge uses FileEncryption HKDF, AES-CTR, then HMAC-SHA256.
func fileKeys(material []byte) (cipher.Block, []byte, []byte, error) {
	if len(material) != 32 {
		return nil, nil, nil, errors.New("invalid attachment key material")
	}
	derived := make([]byte, 76)
	if _, err := io.ReadFull(hkdf.New(sha256.New, material, nil, []byte("FileEncryption")), derived); err != nil {
		return nil, nil, nil, err
	}
	block, err := aes.NewCipher(derived[:32])
	counter := make([]byte, 16)
	copy(counter, derived[64:])
	return block, derived[32:64], counter, err
}
func encryptFile(data []byte) ([]byte, string, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return nil, "", err
	}
	defer clear(material)
	block, macKey, counter, err := fileKeys(material)
	if err != nil {
		return nil, "", err
	}
	encrypted := make([]byte, len(data))
	cipher.NewCTR(block, counter).XORKeyStream(encrypted, data)
	mac := hmac.New(sha256.New, macKey)
	mac.Write(encrypted)
	return append(encrypted, mac.Sum(nil)...), base64.StdEncoding.EncodeToString(material), nil
}
func decryptFile(data []byte, key string) ([]byte, error) {
	material, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, errors.New("invalid attachment key material")
	}
	defer clear(material)
	block, macKey, counter, err := fileKeys(material)
	if err != nil {
		return nil, err
	}
	if len(data) < sha256.Size {
		return nil, errors.New("encrypted attachment is truncated")
	}
	body, tag := data[:len(data)-sha256.Size], data[len(data)-sha256.Size:]
	mac := hmac.New(sha256.New, macKey)
	mac.Write(body)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return nil, errors.New("attachment authentication failed; file was not saved")
	}
	plain := make([]byte, len(body))
	cipher.NewCTR(block, counter).XORKeyStream(plain, body)
	return plain, nil
}

func (c *Client) uploadFile(file *Attachment, metadata map[string]string) ([]byte, error) {
	data, key, err := encryptFile(file.Data)
	if err != nil {
		return nil, err
	}
	var oid string
	if err := c.Session.Mutate(func(api session.API) (err error) { oid, err = api.UploadOBSWithSID(data, "emf"); return }); err != nil {
		return nil, err
	}
	if oid == "" {
		return nil, errors.New("file upload returned no object ID")
	}
	metadata["OID"], metadata["SID"], metadata["ENC_KM"] = oid, "emf", key
	return json.Marshal(map[string]string{"keyMaterial": key, "fileName": file.Name})
}

// DownloadFile only returns authenticated bytes, never key material or OBS URLs.
func (c *Client) DownloadFile(ctx context.Context, chat, id string) ([]byte, error) {
	msg, err := c.findMessage(chat, id)
	if err != nil {
		return nil, err
	}
	if msg.ContentType != 14 {
		return nil, errors.New("download currently supports generic file attachments only")
	}
	if n, err := strconv.ParseInt(msg.ContentMetadata["FILE_SIZE"], 10, 64); err == nil && (n < 0 || n > MaxAttachmentBytes) {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	oid, sid, talkMeta := msg.ContentMetadata["OID"], "emf", msg.ID
	var key string
	if oid == "" {
		if len(msg.Chunks) > 0 || msg.ContentMetadata["e2eeVersion"] != "" {
			return nil, errors.New("encrypted attachment has no object ID")
		}
		oid, sid, talkMeta = msg.ID, "m", ""
	} else {
		payload, err := c.decryptPayload(chat, msg)
		if err != nil {
			return nil, err
		}
		var body struct {
			Key string `json:"keyMaterial"`
		}
		if json.Unmarshal([]byte(payload), &body) != nil || body.Key == "" {
			return nil, errors.New("attachment decryption key is unavailable")
		}
		key = body.Key
	}
	opts := line.OBSDownloadOptions{OBSPop: msg.ContentMetadata["OBS_POP"], MaxBytes: MaxAttachmentBytes + sha256.Size}
	if sid == "m" {
		var info struct {
			Category string `json:"category"`
		}
		if json.Unmarshal([]byte(msg.ContentMetadata["MEDIA_CONTENT_INFO"]), &info) == nil && info.Category == "original" {
			opts.TID = "original"
		}
	}
	var data []byte
	if err := c.Session.Do(func(api session.API) (err error) {
		data, err = api.DownloadOBSWithSIDOptions(ctx, oid, talkMeta, sid, opts)
		return
	}); err != nil {
		return nil, err
	}
	if len(data) > MaxAttachmentBytes+sha256.Size {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	if key != "" {
		data, err = decryptFile(data, key)
		if err != nil {
			return nil, err
		}
	}
	if len(data) > MaxAttachmentBytes {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	return data, nil
}
