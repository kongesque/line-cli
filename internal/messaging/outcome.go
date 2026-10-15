package messaging

import (
	"context"
	"errors"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
)

// MutationError retains message identity, sequence, and every remote mutation
// step. The original cause remains available to storage/signal exit handling.
type MutationError struct {
	Mutation fault.Mutation
	Stage    string
	cause    error
}

func (e *MutationError) Error() string { return e.cause.Error() }
func (e *MutationError) Unwrap() error { return e.cause }

func mutationFailure(op *fault.Mutation, stage string, err error) error {
	if err == nil {
		return nil
	}
	return &MutationError{Mutation: *op, Stage: stage, cause: err}
}

func (c *Client) attempt(ctx context.Context, op *fault.Mutation, stage string, primary bool, call func(session.API) error) error {
	err := c.Session.MutateContext(ctx, call)
	step := fault.Step{Stage: stage, Attempted: true, Outcome: fault.Confirmed}
	if err != nil {
		step.Outcome = fault.Unknown
		var attempt *session.MutationAttemptError
		if errors.As(err, &attempt) {
			step.Attempted, step.Outcome = attempt.Attempted, attempt.Outcome
		}
	}
	op.Steps = append(op.Steps, step)
	if primary {
		op.Attempted, op.Outcome = step.Attempted, step.Outcome
	}
	return err
}
