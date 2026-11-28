package messaging

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
)

var fullUserID = regexp.MustCompile(`^(?:[uU][A-Za-z0-9_-]{43}|u[0-9a-f]{32})$`)

// ValidateUserMID accepts only a full LINE user MID (u/U), rejecting groups,
// rooms, and display names.
func ValidateUserMID(mid string) error {
	if !fullUserID.MatchString(mid) {
		return errors.New("use a full user ID (u/U), not a group/room ID or display name")
	}
	return nil
}

// ContactBlockResult is the receipt for blocking or unblocking a user.
type ContactBlockResult struct {
	Action          string `json:"action"`
	ChatID          string `json:"chat_id"`
	RequestSequence int64  `json:"request_sequence"`
}

// BlockContactContext blocks one user MID with a single persisted request
// sequence. Caller holds Lock.
func BlockContactContext(ctx context.Context, manager *session.Manager, mid string) (*ContactBlockResult, error) {
	return setContactBlocked(ctx, manager, mid, true)
}

// UnblockContactContext unblocks one user MID with a single persisted request
// sequence. Caller holds Lock.
func UnblockContactContext(ctx context.Context, manager *session.Manager, mid string) (*ContactBlockResult, error) {
	return setContactBlocked(ctx, manager, mid, false)
}

func setContactBlocked(ctx context.Context, manager *session.Manager, mid string, blocked bool) (result *ContactBlockResult, resultErr error) {
	action := "block_contact"
	if !blocked {
		action = "unblock_contact"
	}
	op := &fault.Mutation{Action: action, Outcome: fault.NotAttempted}
	stage := "validation"
	defer func() { resultErr = mutationFailure(op, stage, resultErr) }()
	if err := ValidateUserMID(mid); err != nil {
		return nil, err
	}
	op.ChatID = mid
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage = "preparation"
	state, err := manager.Store.Load()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, session.ErrNotFound
	}
	if state.MID != "" && state.MID == mid {
		return nil, fault.New("invalid_arguments", "validation", "Cannot block or unblock your own LINE account.", nil)
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
	if err := c.attempt(ctx, op, stage, true, func(api session.API) error {
		return api.SetContactBlockedContext(ctx, seq, mid, blocked)
	}); err != nil {
		return nil, fmt.Errorf("%s did not return success; check LINE before retrying: %w", action, err)
	}
	return &ContactBlockResult{Action: action, ChatID: mid, RequestSequence: seq}, nil
}
