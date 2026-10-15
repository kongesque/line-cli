package session

import (
	"context"
	"errors"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestMutationAttemptSurvivesRecoveryFailure(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}, saveErr: ErrDurabilityUncertain}
	f := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "new", RefreshToken: "rotated"}}
	m := testManager(s, f)
	calls := 0
	err := m.MutateContext(context.Background(), func(API) error { calls++; return &line.ResponseError{Status: 401} })
	var attempt *MutationAttemptError
	if !errors.As(err, &attempt) || !attempt.Attempted || attempt.Outcome != fault.Rejected || calls != 1 || !errors.Is(err, ErrDurabilityUncertain) || StorageExitCode(err) != 74 {
		t.Fatalf("lost attempt/recovery error: %+v", err)
	}
}
func TestMutationPreparationFailureDoesNotClaimAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := testManager(&memoryStore{}, &fakeAPI{})
	err := m.MutateContext(ctx, func(API) error { t.Fatal("mutation ran"); return nil })
	var attempt *MutationAttemptError
	if !errors.As(err, &attempt) || attempt.Attempted || attempt.Outcome != fault.NotAttempted || !errors.Is(err, context.Canceled) {
		t.Fatalf("bad attempt: %+v", err)
	}
}
