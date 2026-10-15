package messaging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
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

func (c *Client) React(chat, id, reaction string, remove bool) (*ActionResult, error) {
	return c.ReactContext(context.Background(), chat, id, reaction, remove)
}

func (c *Client) ReactContext(ctx context.Context, chat, id, reaction string, remove bool) (*ActionResult, error) {
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
	if _, err := c.findMessage(ctx, chat, id); err != nil {
		return nil, err
	}
	action := "react"
	if remove {
		action = "remove_reaction"
	}
	return c.mutateMessage(ctx, chat, id, action, func(api session.API, seq int64) error {
		if remove {
			return api.CancelReactionContext(ctx, seq, id)
		}
		return api.ReactContext(ctx, seq, id, kind)
	})
}

func (c *Client) Unsend(chat, id string) (*ActionResult, error) {
	return c.UnsendContext(context.Background(), chat, id)
}

func (c *Client) UnsendContext(ctx context.Context, chat, id string) (*ActionResult, error) {
	msg, err := c.findMessage(ctx, chat, id)
	if err != nil {
		return nil, err
	}
	if msg.From != c.state.MID {
		return nil, errors.New("only your own messages can be unsent")
	}
	return c.mutateMessage(ctx, chat, id, "unsend", func(api session.API, seq int64) error { return api.UnsendMessageContext(ctx, seq, id) })
}

func (c *Client) mutateMessage(ctx context.Context, chat, id, action string, call func(session.API, int64) error) (result *ActionResult, resultErr error) {
	op := &fault.Mutation{Action: action, ChatID: chat, MessageID: id, Outcome: fault.NotAttempted}
	stage := "persist_sequence"
	defer func() { resultErr = mutationFailure(op, stage, resultErr) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seq, err := c.Session.ReserveSequence()
	if err != nil {
		return nil, err
	}
	op.RequestSequence = &seq
	stage = action
	if err := c.attempt(ctx, op, stage, true, func(api session.API) error { return call(api, seq) }); err != nil {
		return nil, fmt.Errorf("%s did not return success; check LINE before retrying: %w", action, err)
	}
	return &ActionResult{Action: action, ChatID: chat, MessageID: id, RequestSequence: seq}, nil
}
