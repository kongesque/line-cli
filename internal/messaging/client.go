package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
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
	EncryptMessageV2Raw(string, string, int, string, int, int, int, []byte) ([]string, error)
	EncryptGroupMessageRaw(string, string, int, []byte) ([]string, error)
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
	ChatID      string                 `json:"-"`
	Content     Content                `json:"-"`
	FileName    string                 `json:"file_name,omitempty"`
	ID          string                 `json:"id"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	CreatedTime json.Number            `json:"created_time"`
	ContentType int                    `json:"content_type"`
	Text        string                 `json:"text"`
	Encrypted   bool                   `json:"encrypted"`
	Status      string                 `json:"status"`
	Error       string                 `json:"error,omitempty"`
	ReplyTo     string                 `json:"reply_to,omitempty"`
	Reactions   []line.MessageReaction `json:"reactions,omitempty"`
}

func (c *Client) History(chat string, limit int) ([]Message, error) {
	return c.HistoryContext(context.Background(), chat, limit)
}

func (c *Client) HistoryContext(ctx context.Context, chat string, limit int) ([]Message, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	var raw []*line.Message
	if err := c.Session.DoContext(ctx, func(api session.API) (err error) { raw, err = api.GetRecentMessagesV2Context(ctx, chat, limit); return }); err != nil {
		return nil, err
	}
	result := make([]Message, 0, len(raw))
	for _, msg := range raw {
		if msg == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, c.DecodeContext(ctx, chat, msg))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Decode converts a non-nil history or stream message to safe CLI output.
// The caller holds the session lock; key lookups never register group keys.
func (c *Client) Decode(chat string, msg *line.Message) Message {
	return c.DecodeContext(context.Background(), chat, msg)
}

func (c *Client) DecodeContext(ctx context.Context, chat string, msg *line.Message) Message {
	timestamp := msg.CreatedTime
	if timestamp == "" {
		timestamp = "0"
	}
	item := Message{ID: msg.ID, From: msg.From, To: msg.To, CreatedTime: timestamp, ContentType: msg.ContentType,
		ReplyTo: msg.RelatedMessageID, Reactions: msg.Reactions,
		Encrypted: len(msg.Chunks) > 0 || msg.ContentMetadata["e2eeVersion"] != ""}
	// Storage/encryption markers also prohibit a plaintext content projection when
	// chunks are absent or damaged, matching attachment download validation.
	if IsDownloadable(msg.ContentType) && (msg.ContentMetadata["OID"] != "" || msg.ContentMetadata["ENC_KM"] != "" || strings.HasPrefix(msg.ContentMetadata["SID"], "em")) {
		item.Encrypted = true
	}
	item.ChatID = chat
	// Keep the legacy projection and expose richer decoding separately.
	if IsDownloadable(msg.ContentType) {
		item.Status = "attachment"
		if msg.ContentType == 14 {
			item.FileName = msg.ContentMetadata["FILE_NAME"]
		}
	} else if msg.ContentType != 0 {
		item.Status = "unsupported"
	} else if !item.Encrypted {
		item.Status, item.Text = "plaintext", msg.Text
	} else {
		item.Status = "decrypted"
	}
	payload := ""
	if item.Encrypted && contentKind(msg.ContentType) != "unsupported" {
		var err error
		payload, err = c.decryptPayload(ctx, chat, msg)
		if err != nil {
			item.Content = Content{Kind: contentKind(msg.ContentType), Status: "decryption_failed"}
			if msg.ContentType == 0 {
				item.Status, item.Error = "decryption_failed", err.Error()
			}
			return item
		}
	}
	item.Content = decodeContent(msg, item.Encrypted, payload)
	if item.Encrypted && msg.ContentType == 0 {
		item.Text = item.Content.Text
		if item.Content.Status == "malformed_metadata" && item.Content.Text == "" && item.Content.Location == nil {
			item.Status, item.Error = "decryption_failed", "decrypted payload is not a supported text message"
		}
	}

	return item
}

func (c *Client) decryptPayload(ctx context.Context, chat string, msg *line.Message) (string, error) {
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
		if err := c.peerByID(ctx, msg.From, sender); err != nil {
			return "", err
		}
		if err := c.groupKey(ctx, chat, receiver); err != nil {
			return "", fmt.Errorf("group key unavailable for this historical message: %w", err)
		}
		payload, _, err = c.crypto.DecryptGroupMessage(msg, chat)
	} else {
		peer, key := msg.From, sender
		if c.crypto.IsMyKey(sender) {
			peer, key = msg.To, receiver
		}
		if err := c.peerByID(ctx, peer, key); err != nil {
			return "", err
		}
		payload, err = c.crypto.DecryptMessageV2(msg)
	}
	if err != nil {
		return "", errors.New("letter sealing decryption failed; the required device keys may be unavailable")
	}
	return payload, nil
}

type SendResult struct {
	ID                 string `json:"id"`
	ChatID             string `json:"chat_id"`
	Encrypted          bool   `json:"encrypted"`
	GroupKeyRegistered bool   `json:"group_key_registered"`
	RequestSequence    int64  `json:"request_sequence"`
}

// Send sends exactly once after read-only preparation, encrypting unless the
// account or peer explicitly lacks Letter Sealing support. Caller holds Lock.
func (c *Client) Send(chat, text string) (*SendResult, error) {
	return c.SendContext(context.Background(), chat, text)
}

func (c *Client) SendContext(ctx context.Context, chat, text string) (*SendResult, error) {
	return c.SendReplyContext(ctx, chat, text, "")
}

func (c *Client) SendReply(chat, text, replyTo string) (*SendResult, error) {
	return c.SendReplyContext(context.Background(), chat, text, replyTo)
}

func (c *Client) SendReplyContext(ctx context.Context, chat, text, replyTo string) (*SendResult, error) {
	return c.send(ctx, chat, text, replyTo, nil, nil, nil)
}

// SendMentionsContext sends explicit mentions with the ordinary single-attempt
// text/reply flow. It does not derive mentions from literal @name text.
func (c *Client) SendMentionsContext(ctx context.Context, chat, text, replyTo string, mentions []Mention) (*SendResult, error) {
	return c.send(ctx, chat, text, replyTo, nil, mentions, nil)
}

func (c *Client) SendFile(chat string, file Attachment, replyTo string) (*SendResult, error) {
	return c.SendFileContext(context.Background(), chat, file, replyTo)
}

func (c *Client) SendFileContext(ctx context.Context, chat string, file Attachment, replyTo string) (*SendResult, error) {
	return c.send(ctx, chat, "", replyTo, &file, nil, nil)
}

func (c *Client) send(ctx context.Context, chat, text, replyTo string, file *Attachment, mentions []Mention, image *PreparedImage) (result *SendResult, resultErr error) {
	op := &fault.Mutation{Action: "send", Outcome: fault.NotAttempted}
	stage := "validation"
	defer func() { resultErr = mutationFailure(op, stage, resultErr) }()
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if file == nil {
		if err := ValidateText(text); err != nil {
			return nil, err
		}
	} else if err := file.Validate(); err != nil {
		return nil, err
	}
	if replyTo != "" {
		if err := ValidateMessageID(replyTo); err != nil {
			return nil, err
		}
	}
	mentionMetadata, err := EncodeMentions(text, mentions)
	if err != nil {
		return nil, err
	}
	for _, mention := range mentions {
		if mention.All && toType(chat) == 0 {
			return nil, errors.New("mention-all requires a group or room")
		}
	}
	op.ChatID = chat
	if replyTo != "" {
		stage = "lookup"
		if _, err := c.findMessage(ctx, chat, replyTo); err != nil {
			return nil, err
		}
	}
	stage = "preparation"
	if c.keyError != nil {
		return nil, fault.New("invalid_session", "preparation", c.keyError.Error(), c.keyError)
	}
	kind := toType(chat)
	if kind == 0 {
		var blocked []string
		if err := c.Session.DoContext(ctx, func(api session.API) (err error) { blocked, err = api.GetBlockedContactIdsContext(ctx); return }); err != nil {
			return nil, fmt.Errorf("could not verify blocked contacts; message was not sent: %w", err)
		}
		for _, mid := range blocked {
			if mid == chat {
				return nil, errors.New("contact is blocked on LINE; unblock them on your phone before sending")
			}
		}
	}
	plain := c.state.NoE2EE
	if image != nil && !plain {
		var flow *line.MediaMessageFlowResponse
		if err := c.Session.DoContext(ctx, func(api session.API) (err error) { flow, err = api.DetermineMediaMessageFlowContext(ctx, chat); return }); err != nil {
			return nil, err
		}
		if flow == nil || (flow.FlowMap["1"] != 1 && flow.FlowMap["1"] != 2) {
			return nil, errors.New("LINE returned no supported image upload capability")
		}
		plain = flow.FlowMap["1"] == 1
	}
	groupKeyRegistered := false
	metadata := make(map[string]string)
	if mentionMetadata != "" {
		metadata["MENTION"] = mentionMetadata
	}
	contentType := 0
	if file != nil {
		contentType = 14
		if image != nil {
			contentType = 1
		}
		metadata["FILE_NAME"] = file.Name
		metadata["FILE_SIZE"] = strconv.Itoa(len(file.Data))
		metadata["contentType"] = strconv.Itoa(contentType)
		if image != nil {
			// Chrome's ordinary image envelope has no generic-file metadata.
			clear(metadata)
		}
	}
	var chunks []string
	if !plain && kind == 0 {
		key, keyErr := c.negotiate(ctx, chat)
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
			if file == nil {
				chunks, err = c.crypto.EncryptMessageV2(chat, c.state.MID, runtimeKey, key.PublicKey, own, int(peer), 0, text)
			} else {
				var payload []byte
				payload, err = c.uploadAttachment(ctx, file, image, metadata, op)
				if err == nil {
					chunks, err = c.crypto.EncryptMessageV2Raw(chat, c.state.MID, runtimeKey, key.PublicKey, own, int(peer), contentType, payload)
				}
			}
		}
	} else if !plain {
		err = c.groupKey(ctx, chat, 0)
		if errors.Is(err, errGroupKeyMissing) || errors.Is(err, e2ee.ErrMissingOwnPrivateKey) {
			err = c.registerGroupKey(ctx, chat, op)
			if err == nil {
				groupKeyRegistered = true
				err = c.groupKey(ctx, chat, 0)
			}
		}
		if errors.Is(err, errNoLetterSealing) {
			plain, err = true, nil
		} else if err == nil {
			if file == nil {
				chunks, err = c.crypto.EncryptGroupMessage(chat, c.state.MID, text)
			} else {
				var payload []byte
				payload, err = c.uploadAttachment(ctx, file, image, metadata, op)
				if err == nil {
					chunks, err = c.crypto.EncryptGroupMessageRaw(chat, c.state.MID, contentType, payload)
				}
			}
		}
	}
	if err != nil {
		if len(op.Steps) > 0 && op.Steps[len(op.Steps)-1].Outcome != fault.Confirmed {
			stage = op.Steps[len(op.Steps)-1].Stage
		}
		if file != nil {
			return nil, fmt.Errorf("could not prepare encrypted attachment; no message was sent, but an uploaded file may remain: %w", safePreparationError(err))
		}
		return nil, fmt.Errorf("could not prepare encrypted message; no message was sent: %w", safePreparationError(err))
	}
	if !plain && len(chunks) != 5 {
		return nil, errors.New("encryption returned invalid chunks; nothing was sent")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage = "persist_sequence"
	seq, err := c.Session.ReserveSequence()
	if err != nil {
		return nil, err
	}
	op.RequestSequence = &seq
	now := c.Session.Now().UnixMilli()
	msg := &line.Message{ID: fmt.Sprintf("local-%d", now), From: c.state.MID, To: chat, ToType: kind,
		CreatedTime: json.Number(strconv.FormatInt(now, 10)), ContentType: contentType, HasContent: file != nil, ContentMetadata: metadata}
	if replyTo != "" {
		msg.RelatedMessageID = replyTo
		msg.MessageRelationType = 3
		msg.RelatedMessageServiceCode = 1
	}
	if plain {
		msg.Text = text
	} else {
		msg.Chunks = chunks
		msg.ContentMetadata["e2eeVersion"] = "2"
	}
	var sent *line.Message
	stage = "send"
	err = c.attempt(ctx, op, stage, true, func(api session.API) (err error) { sent, err = api.SendMessageContext(ctx, seq, msg); return })
	if err != nil {
		if op.Outcome == fault.NotAttempted {
			return nil, fmt.Errorf("send was not attempted: %w", err)
		}
		if op.Outcome == fault.Rejected {
			return nil, fmt.Errorf("LINE rejected the send: %w", err)
		}
		return nil, fmt.Errorf("send did not return success; delivery may have occurred; inspect history before retrying: %w", err)
	}
	if sent == nil || sent.ID == "" {
		op.Outcome = fault.Unknown
		op.Steps[len(op.Steps)-1].Outcome = fault.Unknown
		return nil, fault.New("remote_failed", "send", "send returned no message ID; delivery may have occurred; inspect history before retrying", nil)
	}
	op.MessageID = sent.ID
	if plain && file != nil {
		stage = "attachment_upload"
		obsType := "file"
		if image != nil {
			obsType = "image"
		}
		if err := c.attempt(ctx, op, stage, false, func(api session.API) error { return api.UploadOBSPlainContext(ctx, file.Data, sent.ID, obsType) }); err != nil {
			return nil, fmt.Errorf("message %s was created but file upload did not return success; inspect LINE before retrying: %w", sent.ID, err)
		}
	}
	return &SendResult{ID: sent.ID, ChatID: chat, Encrypted: !plain, GroupKeyRegistered: groupKeyRegistered, RequestSequence: seq}, nil
}

// Preserve cancellation without leaking errors from the crypto runtime.
func safePreparationError(err error) error {
	var safe *fault.Error
	if errors.As(err, &safe) {
		return safe
	}
	var attempt *session.MutationAttemptError
	var remote *session.RemoteError
	if errors.As(err, &attempt) || errors.As(err, &remote) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fault.New("encryption_failed", "preparation", "check connectivity, chat membership, and Letter Sealing keys", err)
}
