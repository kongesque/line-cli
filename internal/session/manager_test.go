package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

func TestLoginPreservesKeysAndV3TokensAfterPIN(t *testing.T) {
	s := &memoryStore{}
	f := &fakeAPI{
		loginResults: []*line.LoginResult{{Verifier: "verifier", Pin: "123456"}},
		waitResult: &line.LoginResult{AuthToken: "legacy", Certificate: "cert", E2EEPublicKey: "pub", EncryptedKeyChain: "chain",
			TokenV3IssueResult: &line.TokenV3IssueResult{AccessToken: "v3", RefreshToken: "refresh", DurationUntilRefreshSec: "3600"}},
	}
	m := testManager(s, f)
	var notices int
	_, err := m.Login("a@example.com", "never-store-password", func(pin string, wait bool) error {
		notices++
		if pin != "123456" || wait {
			t.Fatalf("unexpected challenge: %q %v", pin, wait)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if notices != 1 || s.state.AccessToken != "v3" || s.state.RefreshToken != "refresh" || s.state.Certificate != "cert" || s.state.ExportedKeys["123"] != "exported-key" {
		t.Fatal("incomplete saved session")
	}
	data, _ := json.Marshal(s.state)
	if strings.Contains(string(data), "never-store-password") {
		t.Fatal("password persisted")
	}
	if !s.state.RefreshAt.Equal(time.Unix(4570, 0)) {
		t.Fatal("refresh schedule lost")
	}
}

func TestLoginCarriesNoE2EEAcrossPoll(t *testing.T) {
	s := &memoryStore{}
	f := &fakeAPI{loginResults: []*line.LoginResult{{Verifier: "v", NoE2EE: true}}, waitResult: &line.LoginResult{AuthToken: "token"}}
	m := testManager(s, f)
	m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) {
		t.Fatal("export for LSOFF")
		return nil, nil
	}
	_, err := m.Login("email", "password", func(string, bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !f.waitNoE2EE || !s.state.NoE2EE {
		t.Fatal("LSOFF state lost during verification")
	}
}

func TestLoginRejectsMissingKeysWithoutOverwritingSession(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "existing"}}
	f := &fakeAPI{loginResults: []*line.LoginResult{{AuthToken: "new"}}}
	_, err := testManager(s, f).Login("email", "password", func(string, bool) error { return nil })
	if err == nil || s.saves != 0 || s.state.AccessToken != "existing" {
		t.Fatal("saved incomplete E2EE session")
	}
}

func TestCertificateChallengeRequiresContinuation(t *testing.T) {
	s := &memoryStore{}
	f := &fakeAPI{loginResults: []*line.LoginResult{{Certificate: "1234"}, {AuthToken: "token", NoE2EE: true}}}
	_, err := testManager(s, f).Login("email", "password", func(pin string, wait bool) error {
		if pin != "1234" || !wait {
			t.Fatal("expected interactive continuation")
		}
		return nil
	})
	if err != nil || f.loginCalls != 2 {
		t.Fatalf("continuation failed: %v", err)
	}
}

func TestDoRefreshesOnceAndPersistsBeforeRetry(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh", ExportedKeys: map[string]string{"key": "retained"}}}
	f := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "new", RefreshToken: "rotated"}}
	m := testManager(s, f)
	var tokens []string
	m.NewClient = func(token string) API { tokens = append(tokens, token); return f }
	calls := 0
	err := m.Do(func(API) error {
		calls++
		if calls == 1 {
			return errors.New(`{"code":119}`)
		}
		if s.state.AccessToken != "new" || s.state.RefreshToken != "rotated" {
			t.Fatal("retry before durable refresh")
		}
		return nil
	})
	if err != nil || calls != 2 || f.refreshCalls != 1 || strings.Join(tokens, ",") != "old,new" {
		t.Fatalf("recovery failed: %v", err)
	}
	if s.state.ExportedKeys["key"] != "retained" {
		t.Fatal("refresh lost keys")
	}
}

func TestDoStopsOnForcedLogout(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
	f := &fakeAPI{}
	m := testManager(s, f)
	err := m.Do(func(API) error { return errors.New("V3_TOKEN_CLIENT_LOGGED_OUT") })
	if err == nil || f.refreshCalls != 0 || !s.state.Invalidated {
		t.Fatal("forced logout was retried")
	}
	err = m.Do(func(API) error { t.Fatal("invalidated session used"); return nil })
	if err == nil {
		t.Fatal("missing invalidated error")
	}
}

func TestRefreshStorageFailureStopsRequest(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh", RefreshAt: time.Unix(1, 0)}, saveErr: errors.New("keychain denied")}
	f := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "new"}}
	err := testManager(s, f).Do(func(API) error { t.Fatal("request ran before credentials were saved"); return nil })
	if err == nil || s.state.AccessToken != "old" {
		t.Fatal("storage failure ignored")
	}
}

func TestDoDoesNotRetryNetworkErrorsOrExposeResponseBodies(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
	f := &fakeAPI{}
	calls := 0
	err := testManager(s, f).Do(func(API) error { calls++; return errors.New("HTTP 500: secret-token-value") })
	if err == nil || strings.Contains(err.Error(), "secret-token-value") || calls != 1 || f.refreshCalls != 0 {
		t.Fatal("unsafe error handling")
	}
}

func TestDoBoundsRefreshRetries(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
	f := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "new"}}
	calls := 0
	err := testManager(s, f).Do(func(API) error { calls++; return errors.New(`{"code":119}`) })
	if err == nil || calls != 2 || f.refreshCalls != 1 {
		t.Fatal("unbounded refresh retry")
	}
}

func TestMutateNeverReplaysAnAuthFailure(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
	f := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "new"}}
	calls := 0
	err := testManager(s, f).Mutate(func(API) error { calls++; return errors.New(`{"code":119}`) })
	if err == nil || calls != 1 || f.refreshCalls != 1 || s.state.AccessToken != "new" {
		t.Fatal("mutation was replayed")
	}
}

func TestReserveSequencePersistsAndStopsAtProtocolLimit(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "token", LastReqSeq: 2_147_483_646}}
	m := testManager(s, &fakeAPI{})
	seq, err := m.ReserveSequence()
	if err != nil || seq != 2_147_483_647 || s.state.LastReqSeq != seq {
		t.Fatal("sequence not persisted")
	}
	if _, err := m.ReserveSequence(); err == nil {
		t.Fatal("sequence overflow accepted")
	}
}

func TestEmailCompletionCancellationPreservesSession(t *testing.T) {
	s := &memoryStore{state: &State{AccessToken: "old"}}
	f := &fakeAPI{loginResults: []*line.LoginResult{{AuthToken: "new", E2EEPublicKey: "pub", EncryptedKeyChain: "chain"}}}
	m := testManager(s, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) {
		cancel()
		return map[string]string{"key": "synthetic"}, nil
	}
	_, err := m.LoginContext(ctx, "synthetic@example.test", "synthetic", nil)
	var outcome *LoginError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &outcome) || !outcome.Approved || s.state.AccessToken != "old" || s.saves != 0 {
		t.Fatal("email completion failed to honor cancellation", err)
	}
}

func TestStoredSessionHasNoLocalLifetime(t *testing.T) {
	for _, hours := range []int{167, 168, 169, 24 * 365} {
		t.Run(fmt.Sprint(hours), func(t *testing.T) {
			s := &memoryStore{state: &State{AccessToken: "accepted", RefreshToken: "refresh"}}
			f := &fakeAPI{}
			m := testManager(s, f)
			start := m.Now()
			m.Now = func() time.Time { return start.Add(time.Duration(hours) * time.Hour) }
			calls := 0
			if err := m.Do(func(API) error { calls++; return nil }); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || f.refreshCalls != 0 || s.saves != 0 || s.state.Invalidated {
				t.Fatal("locally aged session was refreshed or invalidated without a server instruction")
			}
		})
	}
}

func TestServerSessionRejectionAndRelogin(t *testing.T) {
	for _, response := range []string{
		`API error 400: {"code":10051,"data":{"name":"TalkException","code":8,"reason":"V3_TOKEN_CLIENT_LOGGED_OUT"}}`,
		`API error 401: {"code":10004,"message":"REQUEST_NEED_LOGIN","private":"synthetic-secret"}`,
	} {
		for _, mutate := range []bool{false, true} {
			s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
			f := &fakeAPI{loginResults: []*line.LoginResult{{AuthToken: "new", NoE2EE: true}}}
			m := testManager(s, f)
			call := m.Do
			if mutate {
				call = m.Mutate
			}
			calls := 0
			err := call(func(API) error { calls++; return errors.New(response) })
			if !errors.Is(err, ErrSessionInvalidated) || strings.Contains(err.Error(), "168 hours") || !strings.Contains(err.Error(), "line login") || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("missing safe expiration guidance", err)
			}
			if calls != 1 || f.refreshCalls != 0 || !s.state.Invalidated {
				t.Fatal("terminal rejection retried or not persisted")
			}
			if err := m.Do(func(API) error { t.Fatal("invalidated session reused"); return nil }); !errors.Is(err, ErrSessionInvalidated) {
				t.Fatal(err)
			}
			if _, err := m.Login("synthetic@example.test", "synthetic", nil); err != nil {
				t.Fatal(err)
			}
			if s.state.Invalidated || s.state.AccessToken != "new" {
				t.Fatal("login did not replace invalidated session")
			}
			if err := m.Do(func(API) error { calls++; return nil }); err != nil || calls != 2 {
				t.Fatal("authenticated command did not recover", err)
			}
		}
	}
}

func TestRefreshRejectionRetainsCredentialsWithoutLeakingBody(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
		if scheduled {
			s.state.RefreshAt = time.Unix(1, 0)
		}
		f := &fakeAPI{refreshErr: errors.New("HTTP 401: synthetic-secret")}
		calls := 0
		err := testManager(s, f).Do(func(API) error { calls++; return errors.New(`{"code":119}`) })
		wantCalls := 1
		if scheduled {
			wantCalls = 0
		}
		if err == nil || strings.Contains(err.Error(), "168 hours") || !strings.Contains(err.Error(), "line login") || strings.Contains(err.Error(), "synthetic-secret") || calls != wantCalls || f.refreshCalls != 1 {
			t.Fatal("unsafe or unbounded refresh rejection", err)
		}
		if s.state.AccessToken != "old" || s.saves != 0 {
			t.Fatal("failed refresh changed stored credentials")
		}
	}
}

func TestMutateContextCancellationNeverReplays(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		s := &memoryStore{state: &State{AccessToken: "old", RefreshToken: "refresh"}}
		f := &fakeAPI{}
		m := testManager(s, f)
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := m.MutateContext(ctx, func(API) error {
			calls++
			cancel()
			if confirmed {
				return nil
			}
			return context.Canceled
		})
		if calls != 1 || f.refreshCalls != 0 {
			t.Fatalf("calls=%d refreshes=%d", calls, f.refreshCalls)
		}
		if confirmed && err != nil {
			t.Fatalf("lost confirmation: %v", err)
		}
		if !confirmed && !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	}
}
