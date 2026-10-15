package line

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

// GetPreviousMessagesV2Context follows Chrome 3.7.2's older-history request.
// The anchor may be included in the response. Message IDs remain strings.
// See docs/protocol/endpoints/history.md for source provenance and limitations.
func (c *Client) GetPreviousMessagesV2Context(ctx context.Context, chat string, anchor MessageIDWrapper, count int) ([]*Message, error) {
	id, idErr := strconv.ParseUint(anchor.MessageID, 10, 64)
	delivered, timeErr := anchor.DeliveredTime.Int64()
	if chat == "" || count < 1 || count > 100 || idErr != nil || id == 0 || timeErr != nil || delivered < 0 {
		return nil, errors.New("invalid older-history request")
	}
	request := struct {
		MessageBoxID  string           `json:"messageBoxId"`
		EndMessageID  MessageIDWrapper `json:"endMessageId"`
		MessagesCount int              `json:"messagesCount"`
	}{chat, anchor, count}
	// UNKNOWN (1) is used by Chrome's scroll-to-older-history action.
	resp, err := c.callRPCContext(ctx, "TalkService", "getPreviousMessagesV2WithRequest", request, 1)
	if err != nil {
		return nil, err
	}
	return decodeHistoryResponse(resp, count)
}

func decodeHistoryResponse(resp []byte, count int) ([]*Message, error) {
	var wrapper struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(resp, &wrapper) != nil || wrapper.Code == nil || *wrapper.Code != 0 {
		return nil, errors.New("history request failed")
	}
	// A missing/null payload must not masquerade as the end of history.
	var messages []*Message
	if len(wrapper.Data) == 0 || wrapper.Data[0] != '[' || json.Unmarshal(wrapper.Data, &messages) != nil {
		return nil, errors.New("invalid history response")
	}
	if len(messages) > count {
		return nil, errors.New("history response exceeds requested count")
	}
	return messages, nil
}
