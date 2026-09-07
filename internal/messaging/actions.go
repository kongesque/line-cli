package messaging

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func ValidateMessageID(id string) error {
	if id == "" || strings.HasPrefix(id, "+") {
		return errors.New("use a numeric message ID from line messages")
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return errors.New("use a numeric message ID from line messages")
	}
	return nil
}

func ReactionType(value string) (line.ReactionType, error) {
	for id, name := range line.PredefinedReactionName {
		if strings.EqualFold(name, value) || strings.TrimSuffix(line.PredefinedReactionEmoji[id], "\ufe0f") == strings.TrimSuffix(value, "\ufe0f") {
			return line.ReactionType{PredefinedReactionType: id}, nil
		}
	}
	return line.ReactionType{}, errors.New("reaction must be like, love, laugh, surprise, sad, or angry (or the matching emoji)")
}

type ActionResult struct {
	Action          string `json:"action"`
	ChatID          string `json:"chat_id"`
	MessageID       string `json:"message_id"`
	RequestSequence int64  `json:"request_sequence"`
}

// findMessage restricts mutations to a verified message in the selected chat.
func (c *Client) findMessage(chat, id string) (*line.Message, error) {
	if err := ValidateChatID(chat); err != nil {
		return nil, err
	}
	if err := ValidateMessageID(id); err != nil {
		return nil, err
	}
	var messages []*line.Message
	if err := c.Session.Do(func(api session.API) (err error) { messages, err = api.GetRecentMessagesV2(chat, 100); return }); err != nil {
		return nil, err
	}
	for _, msg := range messages {
		if msg != nil && msg.ID == id {
			return msg, nil
		}
	}
	return nil, errors.New("message was not found in the selected chat's 100 most recent messages")
}

func (c *Client) React(chat, id, reaction string, remove bool) (*ActionResult, error) {
	var kind line.ReactionType
	var err error
	if remove {
		if reaction != "" {
			return nil, errors.New("choose a reaction or --remove, not both")
		}
	} else {
		kind, err = ReactionType(reaction)
		if err != nil {
			return nil, err
		}
	}
	if _, err := c.findMessage(chat, id); err != nil {
		return nil, err
	}
	action := "react"
	if remove {
		action = "remove_reaction"
	}
	return c.mutateMessage(chat, id, action, func(api session.API, seq int64) error {
		if remove {
			return api.CancelReaction(seq, id)
		}
		return api.React(seq, id, kind)
	})
}

func (c *Client) Unsend(chat, id string) (*ActionResult, error) {
	msg, err := c.findMessage(chat, id)
	if err != nil {
		return nil, err
	}
	if msg.From != c.state.MID {
		return nil, errors.New("only your own messages can be unsent")
	}
	return c.mutateMessage(chat, id, "unsend", func(api session.API, seq int64) error { return api.UnsendMessage(seq, id) })
}

func (c *Client) mutateMessage(chat, id, action string, call func(session.API, int64) error) (*ActionResult, error) {
	seq, err := c.Session.ReserveSequence()
	if err != nil {
		return nil, err
	}
	if err := c.Session.Mutate(func(api session.API) error { return call(api, seq) }); err != nil {
		return nil, fmt.Errorf("%s did not return success; check LINE before retrying: %w", action, err)
	}
	return &ActionResult{Action: action, ChatID: chat, MessageID: id, RequestSequence: seq}, nil
}
