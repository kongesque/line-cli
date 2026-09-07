package session

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/highesttt/matrix-line-messenger/pkg/e2ee"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

// API is the portion of the upstream client needed by the first CLI milestone.
type API interface {
	Login(email, password, certificate string) (*line.LoginResult, error)
	WaitForLogin(verifier string, noE2EE bool) (*line.LoginResult, error)
	GetProfile() (*line.Profile, error)
	GetEncryptedIdentityV3() (*line.EncryptedIdentityV3, error)
	RefreshAccessToken(string) (*line.TokenV3IssueResult, error)
	GetAllContactIds() ([]string, error)
	GetContactsV2([]string) (*line.ContactsResponse, error)
	GetMessageBoxes(line.MessageBoxesOptions) (*line.MessageBoxesResponse, error)
}

type Manager struct {
	Store      Store
	NewClient  func(string) API
	ExportKeys func(API, *line.LoginResult) (map[string]string, error)
	Now        func() time.Time
}

func NewManager(store Store) *Manager {
	return &Manager{
		Store:      store,
		NewClient:  func(token string) API { return line.NewClient(token) },
		ExportKeys: exportKeys,
		Now:        time.Now,
	}
}

// Notify displays the phone PIN; when wait is true it must wait for the user's
// confirmation. Verifier flows poll immediately while the phone prompt is shown.
type Notify func(pin string, wait bool) error

func (m *Manager) Login(email, password string, notify Notify) (*line.Profile, error) {
	api := m.NewClient("")
	// Always request a fresh keychain. Certificate-only login may return tokens
	// without the encryption keys needed after restarting this CLI process.
	res, err := api.Login(email, password, "")
	if err != nil {
		return nil, remoteError("login", err)
	}
	noE2EE := false
	for step := 0; step < 5; step++ {
		if res == nil {
			return nil, errors.New("LINE returned an empty login response")
		}
		noE2EE = noE2EE || res.NoE2EE
		token := res.AuthToken
		if res.TokenV3IssueResult != nil && res.TokenV3IssueResult.AccessToken != "" {
			token = res.TokenV3IssueResult.AccessToken
		}
		if token != "" {
			return m.finishLogin(email, token, noE2EE, res)
		}
		if res.Verifier != "" {
			pin := res.PinCode
			if pin == "" {
				pin = res.Pin
			}
			if err := notify(pin, false); err != nil {
				return nil, err
			}
			res, err = api.WaitForLogin(res.Verifier, noE2EE)
		} else if res.Certificate != "" {
			// Some LINE responses put a phone challenge in this field; it is
			// not a reusable certificate until login has completed.
			if err := notify(res.Certificate, true); err != nil {
				return nil, err
			}
			res, err = api.Login(email, password, "")
		} else {
			return nil, errors.New("LINE login is incomplete; no verification challenge was returned")
		}
		if err != nil {
			return nil, remoteError("phone verification", err)
		}
	}
	return nil, errors.New("LINE login did not complete after phone verification; run line login again")
}

func (m *Manager) finishLogin(email, token string, noE2EE bool, res *line.LoginResult) (*line.Profile, error) {
	api := m.NewClient(token)
	profile, err := api.GetProfile()
	if err != nil {
		return nil, remoteError("verify account", err)
	}
	if profile == nil || profile.Mid == "" {
		return nil, errors.New("LINE did not return an account ID")
	}
	s := &State{Version: 1, AccessToken: token, Certificate: res.Certificate,
		MID: profile.Mid, Email: email, NoE2EE: noE2EE}
	if res.TokenV3IssueResult != nil {
		s.RefreshToken = res.TokenV3IssueResult.RefreshToken
		s.RefreshAt = refreshAt(m.Now(), res.TokenV3IssueResult)
	}
	if !noE2EE {
		if res.E2EEPublicKey == "" || res.EncryptedKeyChain == "" {
			return nil, errors.New("LINE login returned no Letter Sealing keys; session was not saved; retry line login")
		}
		s.ExportedKeys, err = m.ExportKeys(api, res)
		if err != nil || len(s.ExportedKeys) == 0 {
			return nil, errors.New("could not export Letter Sealing keys; session was not saved; retry line login")
		}
	}
	if err := m.Store.Save(s); err != nil {
		return nil, err
	}
	return profile, nil
}

func exportKeys(api API, res *line.LoginResult) (map[string]string, error) {
	mgr, err := e2ee.NewManager()
	if err != nil {
		return nil, err
	}
	identity, err := api.GetEncryptedIdentityV3()
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, errors.New("missing encrypted identity")
	}
	if err := mgr.InitStorage(identity.WrappedNonce, identity.KDFParameter1, identity.KDFParameter2); err != nil {
		return nil, err
	}
	return mgr.InitFromLoginKeyChain(res.E2EEPublicKey, res.EncryptedKeyChain)
}

func refreshAt(now time.Time, token *line.TokenV3IssueResult) time.Time {
	seconds, err := strconv.ParseInt(token.DurationUntilRefreshSec, 10, 64)
	if err != nil || seconds <= 0 || seconds > 365*24*60*60 {
		return time.Time{}
	}
	if seconds > 60 {
		seconds -= 30
	}
	return now.Add(time.Duration(seconds) * time.Second)
}

// Do is for read-only calls. The caller must hold Lock across the whole command.
// Retrying mutations will need explicit request-sequence handling in milestone 2.
func (m *Manager) Do(call func(API) error) error {
	s, err := m.Store.Load()
	if err != nil {
		return err
	}
	if s.Invalidated {
		return errors.New("LINE session was logged out; run line login")
	}
	api := m.NewClient(s.AccessToken)
	refreshed := false
	if s.RefreshToken != "" && !s.RefreshAt.IsZero() && !m.Now().Before(s.RefreshAt) {
		api, err = m.refresh(s, api)
		if err != nil {
			return err
		}
		refreshed = true
	}
	err = call(api)
	if err == nil {
		return nil
	}
	if line.IsLoggedOut(err) {
		return m.invalidate(s)
	}
	if !refreshed && line.IsAuthError(err) && s.RefreshToken != "" {
		api, err = m.refresh(s, api)
		if err != nil {
			return err
		}
		err = call(api)
		if line.IsLoggedOut(err) {
			return m.invalidate(s)
		}
	}
	return remoteError("request", err)
}

func (m *Manager) refresh(s *State, api API) (API, error) {
	token, err := api.RefreshAccessToken(s.RefreshToken)
	if line.IsLoggedOut(err) {
		return nil, m.invalidate(s)
	}
	if err != nil {
		return nil, remoteError("token refresh", err)
	}
	if token == nil || token.AccessToken == "" {
		return nil, errors.New("LINE returned an empty refresh token response; run line login")
	}
	s.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		s.RefreshToken = token.RefreshToken
	}
	s.RefreshAt = refreshAt(m.Now(), token)
	// Rotated credentials must reach persistent storage before making requests.
	if err := m.Store.Save(s); err != nil {
		return nil, err
	}
	return m.NewClient(s.AccessToken), nil
}

func (m *Manager) invalidate(s *State) error {
	s.Invalidated = true
	if err := m.Store.Save(s); err != nil {
		return err
	}
	return errors.New("LINE logged out this session, possibly because another Chrome client signed in; run line login")
}

func remoteError(action string, err error) error {
	if err == nil {
		return nil
	}
	if line.IsAuthError(err) {
		return fmt.Errorf("LINE %s requires authentication; run line login", action)
	}
	// Upstream errors may embed raw server bodies and credentials. Never print
	// those bodies; command context gives a useful, non-secret diagnostic.
	return fmt.Errorf("LINE %s failed; check your connection and LINE account settings", action)
}
