package messaging

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// Group invitations use full group IDs: invited chats are not message boxes, so
// names cannot be resolved through the usual chat selector.
var fullGroupID = regexp.MustCompile(`^(?:[cC][A-Za-z0-9_-]{43}|c[0-9a-f]{32})$`)

// ValidateInvitationChat accepts only a full group ID.
func ValidateInvitationChat(chat string) error {
	if !fullGroupID.MatchString(chat) {
		return errors.New("use a full group ID from line invites")
	}
	return nil
}

// InvitationResult is the receipt for an accepted or rejected invitation.
type InvitationResult struct {
	Action          string `json:"action"`
	ChatID          string `json:"chat_id"`
	RequestSequence int64  `json:"request_sequence"`
}

// PendingInvitationsContext reads the account's invited group IDs. It is a
// read: authentication recovery may retry it. Order follows LINE's response;
// duplicates and malformed IDs are dropped.
func PendingInvitationsContext(ctx context.Context, manager *session.Manager) ([]string, error) {
	var response *line.GetAllChatMidsResponse
	if err := manager.DoContext(ctx, func(api session.API) (err error) {
		response, err = api.GetAllChatMidsContext(ctx, false, true)
		return
	}); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("LINE returned no invitation list")
	}
	seen := make(map[string]bool)
	pending := make([]string, 0, len(response.InvitedChatMids))
	for _, mid := range response.InvitedChatMids {
		if ValidateChatID(mid) == nil && !seen[mid] {
			pending = append(pending, mid)
			seen[mid] = true
		}
	}
	return pending, ctx.Err()
}

// AcceptInvitationContext joins one pending group. It never registers or
// fetches a group key; only a later explicit send may register one.
func AcceptInvitationContext(ctx context.Context, manager *session.Manager, chat string) (*InvitationResult, error) {
	return changeInvitation(ctx, manager, chat, "accept_invitation", func(api session.API, seq int64) error {
		return api.AcceptChatInvitationContext(ctx, seq, chat)
	})
}

// RejectInvitationContext declines one pending group invitation.
func RejectInvitationContext(ctx context.Context, manager *session.Manager, chat string) (*InvitationResult, error) {
	return changeInvitation(ctx, manager, chat, "reject_invitation", func(api session.API, seq int64) error {
		return api.RejectChatInvitationContext(ctx, seq, chat)
	})
}

// changeInvitation confirms the ID is pending, reserves a persisted sequence,
// and attempts the change once. Caller holds Lock.
func changeInvitation(ctx context.Context, manager *session.Manager, chat, action string, call func(session.API, int64) error) (result *InvitationResult, resultErr error) {
	op := &fault.Mutation{Action: action, Outcome: fault.NotAttempted}
	stage := "validation"
	defer func() { resultErr = mutationFailure(op, stage, resultErr) }()
	if err := ValidateInvitationChat(chat); err != nil {
		return nil, err
	}
	op.ChatID = chat
	stage = "lookup"
	pending, err := PendingInvitationsContext(ctx, manager)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(pending, chat) {
		return nil, fault.New("invalid_arguments", "lookup", "That group has no pending invitation for this account; run line invites to list them. Nothing was changed.", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage = "persist_sequence"
	seq, err := manager.ReserveSequence()
	if err != nil {
		return nil, err
	}
	op.RequestSequence = &seq
	stage = action
	c := &Client{Session: manager}
	if err := c.attempt(ctx, op, stage, true, func(api session.API) error { return call(api, seq) }); err != nil {
		return nil, fmt.Errorf("%s did not return success; check LINE before retrying: %w", action, err)
	}
	return &InvitationResult{Action: action, ChatID: chat, RequestSequence: seq}, nil
}
