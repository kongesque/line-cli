package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
)

func emailCertState() *State {
	return &State{Version: 1, AccessToken: "old", MID: "u123", Email: "a@example.com", Certificate: "saved-cert",
		CertificateOrigin: "email", ExportedKeys: map[string]string{"7": "saved-key"}}
}

func certTokenOnly() *line.LoginResult {
	return &line.LoginResult{TokenV3IssueResult: &line.TokenV3IssueResult{AccessToken: "v3", RefreshToken: "refresh"}}
}

func TestEmailLoginReusesCertificateAndCheckedKeys(t *testing.T) {
	s := &memoryStore{state: emailCertState()}
	f := &fakeAPI{loginResults: []*line.LoginResult{certTokenOnly()}}
	m := testManager(s, f)
	m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) {
		t.Fatal("no keychain to export")
		return nil, nil
	}
	checked := 0
	m.CheckSavedKeys = func(_ context.Context, _ API, mid string, keys map[string]string) error {
		checked++
		if mid != "u123" || keys["7"] != "saved-key" {
			t.Fatal("checked the wrong keys")
		}
		return nil
	}
	_, err := m.Login("a@example.com", "pw", func(string, bool) error { t.Fatal("unexpected phone prompt"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.loginCerts, ",") != "saved-cert" || checked != 1 {
		t.Fatalf("certificate not sent or keys not checked: %q %d", f.loginCerts, checked)
	}
	if s.state.AccessToken != "v3" || s.state.Certificate != "saved-cert" || s.state.CertificateOrigin != "email" || s.state.ExportedKeys["7"] != "saved-key" {
		t.Fatal("reused session saved incompletely")
	}
}

func TestEmailLoginStaleKeysAreNotSaved(t *testing.T) {
	s := &memoryStore{state: emailCertState()}
	f := &fakeAPI{loginResults: []*line.LoginResult{certTokenOnly()}}
	m := testManager(s, f)
	m.CheckSavedKeys = func(context.Context, API, string, map[string]string) error { return ErrSavedKeysNotCurrent }
	_, err := m.Login("a@example.com", "pw", nil)
	var outcome *LoginError
	if !errors.As(err, &outcome) || !errors.Is(err, ErrSavedKeysNotCurrent) || !strings.Contains(err.Error(), "--require-pin") {
		t.Fatalf("missing guidance: %v", err)
	}
	if s.saves != 0 || s.state.AccessToken != "old" {
		t.Fatal("saved a session with stale keys")
	}
}

func TestEmailLoginCertificateSelection(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate  func(*State)
		options EmailLoginOptions
	}{
		"require pin":   {options: EmailLoginOptions{RequirePIN: true}},
		"other email":   {mutate: func(s *State) { s.Email = "b@example.com" }},
		"qr origin":     {mutate: func(s *State) { s.CertificateOrigin = "qr" }},
		"legacy origin": {mutate: func(s *State) { s.CertificateOrigin = "" }},
		"invalidated":   {mutate: func(s *State) { s.Invalidated = true }},
		"no keys":       {mutate: func(s *State) { s.ExportedKeys = nil }},
	} {
		t.Run(name, func(t *testing.T) {
			state := emailCertState()
			if tc.mutate != nil {
				tc.mutate(state)
			}
			s := &memoryStore{state: state}
			f := &fakeAPI{loginResults: []*line.LoginResult{{Verifier: "v", Pin: "123456"}},
				waitResult: &line.LoginResult{AuthToken: "new", Certificate: "new-cert", E2EEPublicKey: "pub", EncryptedKeyChain: "chain"}}
			m := testManager(s, f)
			m.CheckSavedKeys = func(context.Context, API, string, map[string]string) error {
				t.Fatal("saved keys used after a full login")
				return nil
			}
			if _, err := m.EmailLoginContext(context.Background(), "a@example.com", "pw", tc.options, func(string, bool) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if strings.Join(f.loginCerts, ",") != "" || s.state.Certificate != "new-cert" || s.state.ExportedKeys["123"] != "exported-key" {
				t.Fatalf("certificate sent or fresh login not saved: %q", f.loginCerts)
			}
		})
	}
}

func TestStaleCertificateGetsPINFlowWithFreshKeys(t *testing.T) {
	s := &memoryStore{state: emailCertState()}
	f := &fakeAPI{loginResults: []*line.LoginResult{{Verifier: "v", Pin: "123456"}},
		waitResult: &line.LoginResult{AuthToken: "new", Certificate: "new-cert", E2EEPublicKey: "pub", EncryptedKeyChain: "chain"}}
	m := testManager(s, f)
	m.CheckSavedKeys = func(context.Context, API, string, map[string]string) error {
		t.Fatal("fresh keychain ignored")
		return nil
	}
	prompts := 0
	if _, err := m.Login("a@example.com", "pw", func(string, bool) error { prompts++; return nil }); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 || f.loginCalls != 1 || s.state.Certificate != "new-cert" || s.state.ExportedKeys["123"] != "exported-key" {
		t.Fatal("PIN flow did not replace the stale certificate and keys")
	}
}

func TestCertificateLoginFailureIsNotRepeated(t *testing.T) {
	s := &memoryStore{state: emailCertState()}
	m := testManager(s, &fakeAPI{})
	m.NewClient = func(string) API { return &failingLogin{} }
	_, err := m.Login("a@example.com", "pw", nil)
	if err == nil || !strings.Contains(err.Error(), "--require-pin") || strings.Contains(err.Error(), "synthetic-secret") || s.saves != 0 {
		t.Fatalf("unexpected certificate failure handling: %v", err)
	}
}

type failingLogin struct{ fakeAPI }

func (*failingLogin) Login(string, string, string) (*line.LoginResult, error) {
	return nil, errors.New(`loginV2 failed: synthetic-secret (code 1)`)
}

func TestMatchCurrentKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	other := append([]byte(nil), raw...)
	other[0] ^= 0xff
	std := base64.StdEncoding.EncodeToString(raw)
	current := func(id, public string) *line.E2EEPublicKey {
		return &line.E2EEPublicKey{KeyID: json.Number(id), PublicKey: public}
	}
	for name, tc := range map[string]struct {
		rawID   int
		public  string
		current *line.E2EEPublicKey
		ok      bool
	}{
		"same key":             {7, std, current("7", std), true},
		"other encoding":       {7, std, current("7", base64.RawURLEncoding.EncodeToString(raw)), true},
		"newer key on LINE":    {7, std, current("8", std), false},
		"different public key": {7, std, current("7", base64.StdEncoding.EncodeToString(other)), false},
		"no current key":       {7, std, nil, false},
		"malformed key ID":     {7, std, current("seven", std), false},
		"malformed public key": {7, std, current("7", "not-a-key"), false},
		"unusable saved key":   {7, "", current("7", std), false},
		"missing saved key ID": {0, std, current("0", std), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := matchCurrentKey(tc.rawID, tc.public, tc.current)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, ErrSavedKeysNotCurrent)) {
				t.Fatalf("matchCurrentKey = %v", err)
			}
		})
	}
}

func TestCheckSavedKeysFailsBeforeAskingLINE(t *testing.T) {
	api := &negotiateCounter{}
	err := checkSavedKeys(context.Background(), api, "u123", map[string]string{"7": "not base64!"})
	if err == nil || api.calls != 0 {
		t.Fatalf("unreadable saved keys were not rejected locally: %v, %d calls", err, api.calls)
	}
}

type negotiateCounter struct {
	fakeAPI
	calls int
}

func (n *negotiateCounter) NegotiateE2EEPublicKeyContext(context.Context, string) (*line.E2EEPublicKey, error) {
	n.calls++
	return nil, errors.New("unexpected key lookup")
}
