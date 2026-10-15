package session

import (
	"context"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/pkg/line"
)

// MutationAttemptError preserves the original attempt outcome even if subsequent
// credential recovery fails with a different error. It does not enable retries.
type MutationAttemptError struct {
	Attempted bool
	Outcome   fault.Outcome
	cause     error
}

func (e *MutationAttemptError) Error() string { return e.cause.Error() }
func (e *MutationAttemptError) Unwrap() error { return e.cause }

func (m *Manager) mutateContext(ctx context.Context, call func(API) error) error {
	attempted := false
	outcome := fault.NotAttempted
	err := m.do(ctx, func(api API) error {
		attempted = true
		err := call(api)
		switch {
		case err == nil:
			outcome = fault.Confirmed
		case line.IsAuthoritativeRejection(err):
			outcome = fault.Rejected
		default:
			outcome = fault.Unknown
		}
		return err
	}, false)
	if err == nil {
		return nil
	}
	return &MutationAttemptError{Attempted: attempted, Outcome: outcome, cause: err}
}
