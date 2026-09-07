package session

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

type memoryStore struct {
	state   *State
	saves   int
	saveErr error
}

func (s *memoryStore) Load() (*State, error) {
	if s.state == nil {
		return nil, ErrNotFound
	}
	data, _ := json.Marshal(s.state)
	var copy State
	json.Unmarshal(data, &copy)
	return &copy, nil
}
func (s *memoryStore) Save(state *State) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	data, _ := json.Marshal(state)
	s.state = new(State)
	return json.Unmarshal(data, s.state)
}
func (s *memoryStore) Delete() error { s.state = nil; return nil }

type fakeAPI struct {
	API
	loginResults  []*line.LoginResult
	loginCalls    int
	waitResult    *line.LoginResult
	waitNoE2EE    bool
	refreshResult *line.TokenV3IssueResult
	refreshErr    error
	refreshCalls  int
}

func (f *fakeAPI) Login(email, password, certificate string) (*line.LoginResult, error) {
	if certificate != "" {
		return nil, errors.New("unexpected certificate reuse")
	}
	res := f.loginResults[f.loginCalls]
	f.loginCalls++
	return res, nil
}
func (f *fakeAPI) WaitForLogin(verifier string, noE2EE bool) (*line.LoginResult, error) {
	f.waitNoE2EE = noE2EE
	return f.waitResult, nil
}
func (f *fakeAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u123", DisplayName: "Test"}, nil
}
func (f *fakeAPI) RefreshAccessToken(string) (*line.TokenV3IssueResult, error) {
	f.refreshCalls++
	return f.refreshResult, f.refreshErr
}

func testManager(store *memoryStore, api *fakeAPI) *Manager {
	m := NewManager(store)
	m.NewClient = func(string) API { return api }
	m.Now = func() time.Time { return time.Unix(1000, 0) }
	m.ExportKeys = func(API, *line.LoginResult) (map[string]string, error) {
		return map[string]string{"123": "exported-key"}, nil
	}
	return m
}

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
	m.ExportKeys = func(API, *line.LoginResult) (map[string]string, error) { t.Fatal("export for LSOFF"); return nil, nil }
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
	f := &fakeAPI{}
	calls := 0
	err := testManager(s, f).Mutate(func(API) error { calls++; return errors.New(`{"code":119}`) })
	if err == nil || calls != 1 || f.refreshCalls != 0 {
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
