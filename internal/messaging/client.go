package messaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/e2ee"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

const MaxTextUnits = 10000

var midPattern = regexp.MustCompile(`^[uUcCrR][A-Za-z0-9_-]{2,}$`)

func ValidateChatID(id string) error {
	if !midPattern.MatchString(id) {
		return errors.New("use a LINE chat ID from line chats or line contacts")
	}
	return nil
}

func ValidateText(text string) error {
	if !utf8.ValidString(text) {
		return errors.New("message must contain valid UTF-8 text")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("message must not be empty")
	}
	if len(utf16.Encode([]rune(text))) > MaxTextUnits {
		return fmt.Errorf("message exceeds the CLI limit of %d UTF-16 units", MaxTextUnits)
	}
	return nil
}

func toType(id string) int {
	switch strings.ToLower(id[:1]) {
	case "c":
		return 2
	case "r":
		return 1
	default:
		return 0
	}
}

type Crypto interface {
	LoadMyKeyFromExportedMap(map[string]string) error
	MyKeyIDs() (int, int, error)
	MyPublicKey() (int, string, error)
	IsMyKey(int) bool
	HasPeerPublicKey(int) bool
	RegisterPeerPublicKey(int, string)
	UnwrapGroupSharedKey(string, *line.E2EEGroupSharedKey) (int, error)
	DecryptMessageV2(*line.Message) (string, error)
	DecryptGroupMessage(*line.Message, string) (string, int, error)
	EncryptMessageV2(string, string, int, string, int, int, int, string) ([]string, error)
	EncryptGroupMessage(string, string, string) ([]string, error)
	GenerateGroupKey() (int, error)
	WrapGroupKeyForMember(string, int) (string, error)
}

type Client struct {
	Session   *session.Manager
	state     *session.State
	crypto    Crypto
	keyError  error
	groupKeys map[string]bool
}

func New(manager *session.Manager) (*Client, error) {
	return newClient(manager, func() (Crypto, error) { return e2ee.NewManager() })
}

func newClient(manager *session.Manager, factory func() (Crypto, error)) (*Client, error) {
	s, err := manager.Store.Load()
	if err != nil {
		return nil, err
	}
	if s.Invalidated {
		return nil, errors.New("LINE session was logged out; run line login")
	}
	c := &Client{Session: manager, state: s, groupKeys: make(map[string]bool)}
	if s.NoE2EE {
		return c, nil
	}
	if len(s.ExportedKeys) == 0 {
		c.keyError = errors.New("saved Letter Sealing keys are missing; run line login")
		return c, nil
	}
	c.crypto, err = factory()
	if err == nil {
		err = c.crypto.LoadMyKeyFromExportedMap(s.ExportedKeys)
	}
	if err != nil {
		c.crypto = nil
		c.keyError = errors.New("could not restore Letter Sealing keys; run line login")
	}
	return c, nil
}

type Message struct {
	ID          string      `json:"id"`
	From        string      `json:"from"`
	To          string      `json:"to"`
	CreatedTime json.Number `json:"created_time"`
	ContentType int         `json:"content_type"`
	Text        string      `json:"text"`
	Encrypted   bool        `json:"encrypted"`
	Status      string      `json:"status"`
	Error       string      `json:"error,omitempty"`
}

func (c *Client) History(chat string, limit int) ([]Message, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	var raw []*line.Message
	if err := c.Session.Do(func(api session.API) (err error) { raw, err = api.GetRecentMessagesV2(chat, limit); return }); err != nil {
		return nil, err
	}
	result := make([]Message, 0, len(raw))
	for _, msg := range raw {
		if msg == nil {
			continue
		}
		timestamp := msg.CreatedTime
		if timestamp == "" {
			timestamp = "0"
		}
		item := Message{ID: msg.ID, From: msg.From, To: msg.To, CreatedTime: timestamp, ContentType: msg.ContentType,
			Encrypted: len(msg.Chunks) > 0 || msg.ContentMetadata["e2eeVersion"] != ""}
		if msg.ContentType != 0 {
			item.Status = "unsupported"
		} else if !item.Encrypted {
			item.Status, item.Text = "plaintext", msg.Text
		} else {
			text, err := c.decrypt(chat, msg)
			if err != nil {
				item.Status, item.Error = "decryption_failed", err.Error()
			} else {
				item.Status, item.Text = "decrypted", text
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func (c *Client) decrypt(chat string, msg *line.Message) (string, error) {
	if c.crypto == nil {
		return "", errors.New("letter sealing keys unavailable; run line login")
	}
	if len(msg.Chunks) != 5 {
		return "", errors.New("invalid encrypted message chunks")
	}
	sender, err := e2ee.DecodeKeyID(msg.Chunks[3])
	if err != nil || sender <= 0 {
		return "", errors.New("invalid sender key ID")
	}
	receiver, err := e2ee.DecodeKeyID(msg.Chunks[4])
	if err != nil || receiver <= 0 {
		return "", errors.New("invalid receiver key ID")
	}
	var payload string
	if msg.ToType == 1 || msg.ToType == 2 || toType(chat) != 0 {
		if err := c.peerByID(msg.From, sender); err != nil {
			return "", err
		}
		if err := c.groupKey(chat, receiver); err != nil {
			return "", errors.New("group key unavailable for this historical message")
		}
		payload, _, err = c.crypto.DecryptGroupMessage(msg, chat)
	} else {
		peer, key := msg.From, sender
		if c.crypto.IsMyKey(sender) {
			peer, key = msg.To, receiver
		}
		if err := c.peerByID(peer, key); err != nil {
			return "", err
		}
		payload, err = c.crypto.DecryptMessageV2(msg)
	}
	if err != nil {
		return "", errors.New("letter sealing decryption failed; the required device keys may be unavailable")
	}
	var body struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil || body.Text == nil {
		return "", errors.New("decrypted payload is not a supported text message")
	}
	return *body.Text, nil
}

type SendResult struct {
	ID              string `json:"id"`
	ChatID          string `json:"chat_id"`
	Encrypted       bool   `json:"encrypted"`
	RequestSequence int64  `json:"request_sequence"`
}

// Send sends exactly once after read-only preparation, encrypting unless the
// account or peer explicitly lacks Letter Sealing support. Caller holds Lock.
func (c *Client) Send(chat, text string) (*SendResult, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if err := ValidateText(text); err != nil {
		return nil, err
	}
	if c.keyError != nil {
		return nil, c.keyError
	}
	kind := toType(chat)
	if kind == 0 {
		var blocked []string
		if err := c.Session.Do(func(api session.API) (err error) { blocked, err = api.GetBlockedContactIds(); return }); err != nil {
			return nil, errors.New("could not verify blocked contacts; message was not sent")
		}
		for _, mid := range blocked {
			if mid == chat {
				return nil, errors.New("contact is blocked on LINE; unblock them on your phone before sending")
			}
		}
	}
	plain := c.state.NoE2EE
	var chunks []string
	var err error
	if !plain && kind == 0 {
		key, keyErr := c.negotiate(chat)
		if errors.Is(keyErr, errNoLetterSealing) {
			plain = true
		} else if keyErr != nil {
			return nil, keyErr
		} else {
			own, runtimeKey, ownErr := c.crypto.MyKeyIDs()
			if ownErr != nil {
				return nil, errors.New("own Letter Sealing key is unavailable; run line login")
			}
			peer, _ := key.KeyID.Int64()
			chunks, err = c.crypto.EncryptMessageV2(chat, c.state.MID, runtimeKey, key.PublicKey, own, int(peer), 0, text)
		}
	} else if !plain {
		err = c.groupKey(chat, 0)
		if errors.Is(err, errGroupKeyMissing) || errors.Is(err, e2ee.ErrMissingOwnPrivateKey) {
			err = c.registerGroupKey(chat)
			if err == nil {
				err = c.groupKey(chat, 0)
			}
		}
		if errors.Is(err, errNoLetterSealing) {
			plain, err = true, nil
		} else if err == nil {
			chunks, err = c.crypto.EncryptGroupMessage(chat, c.state.MID, text)
		}
	}
	if err != nil {
		return nil, errors.New("could not prepare encrypted message; nothing was sent; check chat membership and Letter Sealing keys")
	}
	if !plain && len(chunks) != 5 {
		return nil, errors.New("encryption returned invalid chunks; nothing was sent")
	}
	seq, err := c.Session.ReserveSequence()
	if err != nil {
		return nil, err
	}
	now := c.Session.Now().UnixMilli()
	msg := &line.Message{ID: fmt.Sprintf("local-%d", now), From: c.state.MID, To: chat, ToType: kind,
		CreatedTime: json.Number(strconv.FormatInt(now, 10)), ContentType: 0, ContentMetadata: make(map[string]string)}
	if plain {
		msg.Text = text
	} else {
		msg.Chunks = chunks
		msg.ContentMetadata["e2eeVersion"] = "2"
	}
	var sent *line.Message
	err = c.Session.Mutate(func(api session.API) (err error) { sent, err = api.SendMessage(seq, msg); return })
	if err != nil {
		return nil, fmt.Errorf("send did not return success; delivery may have occurred; inspect history before retrying: %w", err)
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("send returned no message ID; delivery may have occurred; inspect history before retrying")
	}
	return &SendResult{ID: sent.ID, ChatID: chat, Encrypted: !plain, RequestSequence: seq}, nil
}
