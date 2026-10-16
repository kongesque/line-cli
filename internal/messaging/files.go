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
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/hkdf"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const MaxAttachmentBytes = 20 << 20

// IsDownloadable reports whether a message type has a supported OBS attachment.
func IsDownloadable(contentType int) bool { return attachmentSID(contentType) != "" }

func attachmentSID(contentType int) string {
	switch contentType {
	case 1:
		return "emi"
	case 2:
		return "emv"
	case 3:
		return "ema"
	case 14:
		return "emf"
	default:
		return ""
	}
}

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

// LINE file encryption uses FileEncryption HKDF, AES-CTR, then HMAC-SHA256.
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
	data, err := encryptFileWithMaterial(data, material)
	return data, base64.StdEncoding.EncodeToString(material), err
}

func encryptFileWithMaterial(data, material []byte) ([]byte, error) {
	block, macKey, counter, err := fileKeys(material)
	if err != nil {
		return nil, err
	}
	encrypted := make([]byte, len(data))
	cipher.NewCTR(block, counter).XORKeyStream(encrypted, data)
	mac := hmac.New(sha256.New, macKey)
	mac.Write(encrypted)
	return append(encrypted, mac.Sum(nil)...), nil
}
func decryptFile(data []byte, key string) ([]byte, error) {
	return decryptAttachment(data, key, false)
}

// Full videos authenticate hashes of 128 KiB ciphertext chunks. Other media
// authenticate the ciphertext directly. See docs/protocol/endpoints/media-download.md.
func decryptAttachment(data []byte, key string, video bool) ([]byte, error) {
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
	if video {
		const chunkSize = 128 << 10
		for offset := 0; offset < len(body); offset += chunkSize {
			digest := sha256.Sum256(body[offset:min(offset+chunkSize, len(body))])
			mac.Write(digest[:])
		}
	} else {
		mac.Write(body)
	}
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return nil, errors.New("attachment authentication failed; no data was output")
	}
	plain := make([]byte, len(body))
	cipher.NewCTR(block, counter).XORKeyStream(plain, body)
	return plain, nil
}

func (c *Client) uploadFile(ctx context.Context, file *Attachment, metadata map[string]string, op *fault.Mutation) ([]byte, error) {
	data, key, err := encryptFile(file.Data)
	if err != nil {
		return nil, err
	}
	var oid string
	if err := c.attempt(ctx, op, "attachment_upload", false, func(api session.API) (err error) { oid, err = api.UploadOBSWithSIDContext(ctx, data, "emf"); return }); err != nil {
		return nil, fmt.Errorf("attachment upload did not return success; an uploaded file may remain; inspect LINE before retrying: %w", err)
	}
	if oid == "" {
		op.Steps[len(op.Steps)-1].Outcome = fault.Unknown
		return nil, errors.New("file upload returned no object ID")
	}
	metadata["OID"], metadata["SID"], metadata["ENC_KM"] = oid, "emf", key
	return json.Marshal(map[string]string{"keyMaterial": key, "fileName": file.Name})
}

// DownloadFile downloads images, videos, audio, and generic files. Encrypted
// attachments are authenticated before returning bytes; keys/URLs stay private.
func (c *Client) DownloadFile(ctx context.Context, chat, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	msg, err := c.findMessage(ctx, chat, id)
	if err != nil {
		return nil, err
	}
	if !IsDownloadable(msg.ContentType) {
		return nil, fault.New("unsupported_content", "lookup", "download supports image, video, audio, and file messages only", nil)
	}
	if msg.ContentMetadata["DOWNLOAD_URL"] != "" {
		return nil, fault.New("unsupported_content", "lookup", "downloads from external media URLs are not supported", nil)
	}
	if n, err := strconv.ParseInt(msg.ContentMetadata["FILE_SIZE"], 10, 64); err == nil && (n < 0 || n > MaxAttachmentBytes) {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	oid, sid, talkMeta := msg.ContentMetadata["OID"], attachmentSID(msg.ContentType), msg.ID
	advertisedSID := msg.ContentMetadata["SID"]
	var key string
	if oid == "" {
		if len(msg.Chunks) > 0 || msg.ContentMetadata["e2eeVersion"] != "" || msg.ContentMetadata["ENC_KM"] != "" || strings.HasPrefix(advertisedSID, "em") {
			return nil, errors.New("encrypted attachment has no object ID")
		}
		if advertisedSID != "" && advertisedSID != "m" {
			return nil, errors.New("attachment storage metadata does not match its content type")
		}
		oid, sid, talkMeta = msg.ID, "m", ""
	} else {
		if advertisedSID != "" && advertisedSID != sid {
			return nil, errors.New("encrypted attachment storage metadata does not match its content type")
		}
		payload, err := c.decryptPayload(ctx, chat, msg)
		if err != nil {
			return nil, fault.New("decryption_failed", "decode", "Could not decrypt the attachment metadata.", err)
		}
		var body struct {
			Key string `json:"keyMaterial"`
		}
		if json.Unmarshal([]byte(payload), &body) != nil || body.Key == "" {
			return nil, errors.New("attachment decryption key is unavailable")
		}
		key = body.Key
		material, err := base64.StdEncoding.DecodeString(key)
		validKey := err == nil && len(material) == 32
		clear(material)
		if !validKey {
			return nil, errors.New("invalid attachment key material")
		}
		if advertisedKey := msg.ContentMetadata["ENC_KM"]; advertisedKey != "" && advertisedKey != key {
			return nil, errors.New("encrypted attachment key metadata is inconsistent")
		}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.Session.DoContext(ctx, func(api session.API) (err error) {
		data, err = api.DownloadOBSWithSIDOptions(ctx, oid, talkMeta, sid, opts)
		return
	}); err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return nil, context.DeadlineExceeded
		case errors.Is(err, line.ErrOBSObjectNotFound):
			return nil, errors.New("attachment is no longer available from LINE")
		case errors.Is(err, line.ErrOBSEncodingIncomplete):
			return nil, errors.New("LINE is still processing this attachment; try again later")
		case errors.Is(err, line.ErrOBSSizeLimit):
			return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
		}
		return nil, err
	}
	if len(data) > MaxAttachmentBytes+sha256.Size {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	if key != "" {
		data, err = decryptAttachment(data, key, msg.ContentType == 2)
		if err != nil {
			return nil, fault.New("decryption_failed", "decode", "Could not authenticate or decrypt the attachment.", err)
		}
	}
	if len(data) > MaxAttachmentBytes {
		return nil, errors.New("attachment exceeds the CLI limit of 20 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
