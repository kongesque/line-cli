package messaging

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const (
	LookupMaxPages = 10
	LookupPageSize = 100
	LookupTimeout  = 30 * time.Second
)

type historyCursor struct {
	Version       int    `json:"v"`
	ChatID        string `json:"chat_id"`
	MessageID     string `json:"message_id"`
	DeliveredTime string `json:"delivered_time"`
}

func invalidCursor() error {
	return fault.New("invalid_cursor", "validation", "Invalid history cursor or cursor belongs to a different chat.", nil)
}

func parseHistoryCursor(token string) (*historyCursor, error) {
	if token == "" || len(token) > 1024 {
		return nil, invalidCursor()
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return nil, invalidCursor()
	}
	var cursor historyCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, invalidCursor()
	}
	id, idErr := strconv.ParseUint(cursor.MessageID, 10, 64)
	delivered, timeErr := strconv.ParseInt(cursor.DeliveredTime, 10, 64)
	if cursor.Version != 1 || ValidateChatID(cursor.ChatID) != nil || idErr != nil || id == 0 || strconv.FormatUint(id, 10) != cursor.MessageID || timeErr != nil || delivered < 0 || strconv.FormatInt(delivered, 10) != cursor.DeliveredTime {
		return nil, invalidCursor()
	}
	return &cursor, nil
}

// ValidateHistoryCursor validates locally, before opening session storage. An
// empty chat checks syntax only; callers using names bind it after resolution.
func ValidateHistoryCursor(token, chat string) error {
	cursor, err := parseHistoryCursor(token)
	if err != nil {
		return err
	}
	if chat != "" && cursor.ChatID != chat {
		return invalidCursor()
	}
	return nil
}

func cursorFor(chat string, msg *line.Message) (string, error) {
	cursor := historyCursor{1, chat, msg.ID, msg.DeliveredTime.String()}
	data, _ := json.Marshal(cursor)
	token := base64.RawURLEncoding.EncodeToString(data)
	if _, err := parseHistoryCursor(token); err != nil {
		return "", fault.New("history_invalid", "lookup", "History has no valid delivered-time anchor; continuation is unavailable.", nil)
	}
	return token, nil
}

type HistoryPage struct {
	SchemaVersion int       `json:"schema_version"`
	ChatID        string    `json:"chat_id"`
	Order         string    `json:"order"`
	Messages      []Message `json:"messages"`
	NextCursor    string    `json:"next_cursor,omitempty"`
	Exhausted     bool      `json:"exhausted"`
	StopReason    string    `json:"stop_reason"`
}

// HistoryPageContext performs at most one logical history read. Authentication
// recovery may retry reads. Legacy HistoryContext retains its array contract.
func (c *Client) HistoryPageContext(ctx context.Context, chat string, limit int, token string) (*HistoryPage, error) {
	raw, err := c.historyPage(ctx, chat, limit, token)
	if err != nil {
		return nil, err
	}
	page := &HistoryPage{SchemaVersion: 1, ChatID: chat, Order: "newest_first", Messages: make([]Message, 0, len(raw)), Exhausted: len(raw) == 0, StopReason: "end_of_available_history"}
	if len(raw) > 0 {
		page.StopReason = "page_limit"
		if len(raw) == limit {
			page.StopReason = "count_limit"
		}
		page.NextCursor, err = cursorFor(chat, raw[len(raw)-1])
		if err != nil {
			return nil, err
		}
	}
	for _, msg := range raw {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page.Messages = append(page.Messages, c.DecodeContext(ctx, chat, msg))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func (c *Client) messageInChat(chat string, msg *line.Message) bool {
	if msg == nil || c.state.MID == "" {
		return false
	}
	kind := toType(chat)
	if msg.ToType != kind {
		return false
	}
	if kind != 0 {
		return msg.To == chat && msg.From != ""
	}
	return (msg.From == c.state.MID && msg.To == chat) || (msg.From == chat && msg.To == c.state.MID)
}

func (c *Client) historyPage(ctx context.Context, chat string, limit int, token string) ([]*line.Message, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	var cursor *historyCursor
	var err error
	count := limit
	if token != "" {
		cursor, err = parseHistoryCursor(token)
		if err != nil {
			return nil, err
		}
		if cursor.ChatID != chat {
			return nil, invalidCursor()
		}
		// Allow space for an inclusive anchor, including when the caller wants one
		// message. Never exceed Chrome's request cap.
		count = min(limit+1, 100)
	}
	var raw []*line.Message
	err = c.Session.DoContext(ctx, func(api session.API) (err error) {
		if cursor == nil {
			raw, err = api.GetRecentMessagesV2Context(ctx, chat, count)
		} else {
			raw, err = api.GetPreviousMessagesV2Context(ctx, chat, line.MessageIDWrapper{MessageID: cursor.MessageID, DeliveredTime: json.Number(cursor.DeliveredTime)}, count)
		}
		return
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	badPage := func() ([]*line.Message, error) {
		return nil, fault.New("history_invalid", "lookup", "History returned malformed, mismatched, or nonadvancing messages.", nil)
	}
	if len(raw) > count {
		return badPage()
	}
	result := make([]*line.Message, 0, len(raw))
	seen := make(map[string]*line.Message)
	var anchorID uint64
	if cursor != nil {
		anchorID, _ = strconv.ParseUint(cursor.MessageID, 10, 64)
	}
	for _, msg := range raw {
		if !c.messageInChat(chat, msg) {
			return badPage()
		}
		id, err := strconv.ParseUint(msg.ID, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != msg.ID {
			return badPage()
		}
		if cursor != nil {
			if id > anchorID {
				return badPage()
			}
			if id == anchorID {
				if msg.DeliveredTime.String() != cursor.DeliveredTime {
					return badPage()
				}
				continue
			}
		}
		if previous, ok := seen[msg.ID]; ok {
			if previous.DeliveredTime != msg.DeliveredTime {
				return badPage()
			}
			continue
		}
		seen[msg.ID] = msg
		result = append(result, msg)
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := strconv.ParseUint(result[i].ID, 10, 64)
		b, _ := strconv.ParseUint(result[j].ID, 10, 64)
		return a > b
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// findMessage searches only available history in the selected chat. It never
// decrypts or registers keys, and all four action paths share this boundary.
func (c *Client) findMessage(ctx context.Context, chat, id string) (*line.Message, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if err := ValidateMessageID(id); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	token := ""
	scanned := 0
	for page := 0; page < LookupMaxPages; page++ {
		raw, err := c.historyPage(ctx, chat, LookupPageSize, token)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fault.New("lookup_incomplete", "lookup", fmt.Sprintf("History lookup deadline expired after %d completed pages and %d messages; the target's absence is not established.", page, scanned), err)
			}
			return nil, err
		}
		scanned += len(raw)
		for _, msg := range raw {
			if msg.ID == id {
				return msg, nil
			}
		}
		if len(raw) == 0 {
			return nil, fault.New("message_not_found", "lookup", fmt.Sprintf("Message was not found in available history of the selected chat (%d messages checked); older history may be unavailable.", scanned), nil)
		}
		if page+1 == LookupMaxPages {
			break
		}
		token, err = cursorFor(chat, raw[len(raw)-1])
		if err != nil {
			return nil, err
		}
	}
	return nil, fault.New("lookup_incomplete", "lookup", fmt.Sprintf("Message was not found within the lookup budget (%d pages, %d messages checked; at most 1000 records and 30 seconds).", LookupMaxPages, scanned), nil)
}
