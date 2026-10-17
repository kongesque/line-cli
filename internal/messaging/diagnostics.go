package messaging

import (
	"context"
	"strconv"
)

// HistoryDiagnostics describes the recent-history response before decryption.
// IDs are the only message fields exposed; malformed IDs are counted, not echoed.
type HistoryDiagnostics struct {
	SchemaVersion  int      `json:"schema_version"`
	ChatID         string   `json:"chat_id"`
	Method         string   `json:"method"`
	RequestedCount int      `json:"requested_count"`
	ReturnedCount  int      `json:"returned_count"`
	NullCount      int      `json:"null_count"`
	InvalidIDCount int      `json:"invalid_id_count"`
	MessageIDs     []string `json:"message_ids"`
}

// HistoryDiagnosticsContext makes one logical read (auth recovery may retry),
// without decrypting content, looking up keys, or mutating LINE state.
func (c *Client) HistoryDiagnosticsContext(ctx context.Context, chat string, limit int) (*HistoryDiagnostics, error) {
	raw, err := c.recentMessages(ctx, chat, limit)
	if err != nil {
		return nil, err
	}
	d := &HistoryDiagnostics{SchemaVersion: 1, ChatID: chat, Method: "getRecentMessagesV2", RequestedCount: limit, ReturnedCount: len(raw), MessageIDs: make([]string, 0, len(raw))}
	for _, msg := range raw {
		if msg == nil {
			d.NullCount++
			continue
		}
		id, err := strconv.ParseUint(msg.ID, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != msg.ID {
			d.InvalidIDCount++
			continue
		}
		d.MessageIDs = append(d.MessageIDs, msg.ID)
	}
	return d, ctx.Err()
}
