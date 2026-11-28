package session

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

func refreshAt(now time.Time, token *line.TokenV3IssueResult) time.Time {
	seconds, err := strconv.ParseInt(token.DurationUntilRefreshSec, 10, 64)
	if err != nil || seconds <= 0 || seconds > 365*24*60*60 {
		return time.Time{}
	}
	issued := now
	if epoch, err := strconv.ParseInt(token.TokenIssueTimeEpochSec, 10, 64); err == nil && epoch > 0 && epoch < 253402300800 {
		issued = time.Unix(epoch, 0)
	}
	// Short-lived tokens retain at least half their lifetime before refreshing.
	margin := min(30*time.Second, time.Duration(seconds)*time.Second/2)
	return issued.Add(time.Duration(seconds)*time.Second - margin)
}

// RefreshDeadline prefers server metadata. Legacy sessions retain their saved
// deadline; never derive a fresh now+duration deadline every time they are loaded.
func (s *State) RefreshDeadline() time.Time {
	if epoch, err := strconv.ParseInt(s.TokenIssueTimeEpochSec, 10, 64); err == nil && epoch > 0 && epoch < 253402300800 {
		deadline := refreshAt(time.Time{}, &line.TokenV3IssueResult{
			DurationUntilRefreshSec: s.DurationUntilRefreshSec,
			TokenIssueTimeEpochSec:  s.TokenIssueTimeEpochSec,
		})
		if !deadline.IsZero() {
			return deadline
		}
	}
	return s.RefreshAt
}

func (s *State) applyToken(now time.Time, token *line.TokenV3IssueResult) {
	if token.AccessToken != "" {
		s.AccessToken = token.AccessToken
	}
	if token.RefreshToken != "" {
		s.RefreshToken = token.RefreshToken
	}
	s.DurationUntilRefreshSec = token.DurationUntilRefreshSec
	s.TokenIssueTimeEpochSec = token.TokenIssueTimeEpochSec
	s.RefreshAt = refreshAt(now, token)
	if token.RefreshApiRetryPolicy != nil {
		policy := *token.RefreshApiRetryPolicy
		s.RefreshApiRetryPolicy = &policy
	}
}

// Chrome's refresh endpoint accepts retryCount. Keep the legacy API interface
// for embedders/test doubles while using cancellable requests in the real client.
type refreshAPI interface {
	RefreshAccessTokenContext(context.Context, string, int) (*line.TokenV3IssueResult, error)
}

func (m *Manager) refresh(ctx context.Context, s *State, api API) (API, error) {
	var token *line.TokenV3IssueResult
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if client, ok := api.(refreshAPI); ok {
			token, err = client.RefreshAccessTokenContext(ctx, s.RefreshToken, attempt)
		} else {
			token, err = api.RefreshAccessToken(s.RefreshToken)
		}
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if line.IsLoggedOut(err) {
			return nil, m.invalidate(s, err)
		}
		if attempt == 3 || !line.IsTransientError(err) {
			return nil, remoteError("token refresh", err)
		}
		wait := m.Wait
		if wait == nil {
			wait = waitRefresh
		}
		if err := wait(ctx, refreshDelay(s.RefreshApiRetryPolicy, attempt, rand.Float64())); err != nil {
			return nil, err
		}
	}
	if token == nil || token.AccessToken == "" {
		return nil, remoteError("token refresh", errors.New("empty token response"))
	}
	// Save the complete state as one storage transaction before exposing a new
	// client. A failed/uncertain commit stops requests and never repeats refresh.
	next := *s
	next.applyToken(m.Now(), token)
	if err := m.Store.Save(&next); err != nil {
		return nil, err
	}
	*s = next
	return m.NewClient(s.AccessToken), nil
}

func waitRefresh(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Four attempts, each delay at most 30s, regardless of malformed server values.
// Jitter is symmetric around the exponential delay and capped after applying it.
func refreshDelay(policy *line.RefreshApiRetryPolicy, attempt int, random float64) time.Duration {
	initial, maximum, multiplier, jitter := 1000.0, 30000.0, 2.0, 0.2
	if policy != nil {
		if n, err := strconv.ParseInt(policy.InitialDelayInMillis, 10, 64); err == nil && n > 0 {
			initial = min(float64(n), 30000)
		}
		if n, err := strconv.ParseInt(policy.MaxDelayInMillis, 10, 64); err == nil && n > 0 {
			maximum = min(float64(n), 30000)
		}
		if !math.IsNaN(policy.Multiplier) && !math.IsInf(policy.Multiplier, 0) && policy.Multiplier >= 1 {
			multiplier = min(policy.Multiplier, 100)
		}
		if !math.IsNaN(policy.JitterRate) && policy.JitterRate >= 0 && policy.JitterRate <= 1 {
			jitter = policy.JitterRate
		}
	}
	delay := min(initial*math.Pow(multiplier, float64(attempt)), maximum)
	delay *= 1 + jitter*(2*random-1)
	return time.Duration(max(1, min(delay, maximum))) * time.Millisecond
}
