package messaging

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// ReadScanCount is how many recent messages are considered when no message is
// chosen. Chrome normally loads 50 messages when a chat opens.
const ReadScanCount = 50

// MarkReadContext sends one read receipt, up to id or, when id is empty, up to
// the newest message from someone else in recent history. It is the only
// caller of sendChatChecked. Messages are not decrypted and no keys are
// fetched. Caller holds Lock. See docs/protocol/endpoints/read-receipts.md.
func (c *Client) MarkReadContext(ctx context.Context, chat, id string) (*ActionResult, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	var target *line.Message
	if id != "" {
		msg, err := c.findMessage(ctx, chat, id)
		if err != nil {
			return nil, err
		}
		// Chrome never sends a receipt for the account's own message.
		if msg.From == c.state.MID {
			return nil, fault.New("invalid_arguments", "lookup", "Your own messages can't be marked as read; choose a message from someone else.", nil)
		}
		target = msg
	} else {
		raw, err := c.recentMessages(ctx, chat, ReadScanCount)
		if err != nil {
			return nil, err
		}
		target = c.newestReadable(chat, raw)
		if target == nil {
			return nil, fault.New("message_not_found", "lookup", fmt.Sprintf("No message from someone else among the latest %d messages; nothing was marked as read.", ReadScanCount), nil)
		}
	}
	messageID := target.ID
	return c.mutateMessage(ctx, chat, messageID, "read", func(api session.API, seq int64) error {
		return api.SendChatCheckedContext(ctx, seq, chat, messageID)
	})
}

// newestReadable mirrors Chrome's choice: the largest numeric ID in this chat
// that is not from the account and not marked SKIP_BADGE_COUNT.
func (c *Client) newestReadable(chat string, messages []*line.Message) *line.Message {
	var best *line.Message
	var bestID uint64
	for _, msg := range messages {
		if !c.messageInChat(chat, msg) || msg.From == c.state.MID || msg.ContentMetadata["SKIP_BADGE_COUNT"] == "true" {
			continue
		}
		if ValidateMessageID(msg.ID) != nil {
			continue
		}
		n, _ := strconv.ParseUint(msg.ID, 10, 64)
		if best == nil || n > bestID {
			best, bestID = msg, n
		}
	}
	return best
}
