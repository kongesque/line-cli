package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
)

// API is the portion of the upstream client used by the standalone CLI.
type API interface {
	Login(email, password, certificate string) (*line.LoginResult, error)
	WaitForLogin(verifier string, noE2EE bool) (*line.LoginResult, error)
	GetProfile() (*line.Profile, error)
	GetProfileContext(context.Context) (*line.Profile, error)
	GetLastOpRevisionContext(context.Context) (int64, error)
	ListenSSE(context.Context, int64, func(string, string)) error
	GetEncryptedIdentityV3() (*line.EncryptedIdentityV3, error)
	RefreshAccessToken(string) (*line.TokenV3IssueResult, error)
	GetAllContactIds() ([]string, error)
	GetContactsV2([]string) (*line.ContactsResponse, error)
	GetMessageBoxes(line.MessageBoxesOptions) (*line.MessageBoxesResponse, error)
	GetRecentMessagesV2(string, int) ([]*line.Message, error)
	GetBlockedContactIds() ([]string, error)
	NegotiateE2EEPublicKey(string) (*line.E2EEPublicKey, error)
	GetE2EEPublicKey(string, int, int) (*line.E2EEPublicKey, error)
	GetE2EEGroupSharedKey(string, int) (*line.E2EEGroupSharedKey, error)
	GetLastE2EEGroupSharedKey(string) (*line.E2EEGroupSharedKey, error)
	GetChats([]string, bool, bool) (*line.GetChatsResponse, error)
	RegisterE2EEGroupKey(int, string, []string, []int, []string) error
	SendMessage(int64, *line.Message) (*line.Message, error)
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
	s := &State{Generation: rand.Text(), Version: 1, AccessToken: token, Certificate: res.Certificate,
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
func (m *Manager) Do(call func(API) error) error {
	return m.do(call, true)
}

// Mutate refreshes expired credentials before the call but never replays it.
// A lost response may mean the server already accepted the mutation.
func (m *Manager) Mutate(call func(API) error) error {
	return m.do(call, false)
}

func (m *Manager) do(call func(API) error, retry bool) error {
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
	if retry && !refreshed && line.IsAuthError(err) && s.RefreshToken != "" {
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

// MarkLoggedOut invalidates the current session without attempting token refresh.
// The caller holds Lock and has verified the failing stream used this session.
func (m *Manager) MarkLoggedOut() error {
	s, err := m.Store.Load()
	if err != nil {
		return err
	}
	return m.invalidate(s)
}

func remoteError(action string, err error) error {
	if err == nil {
		return nil
	}
	return &RemoteError{Action: action, cause: err}
}

// RemoteError preserves protocol classification internally, while its printable
// message never contains raw response bodies, tokens, or message contents.
type RemoteError struct {
	Action string
	cause  error
}

func (e *RemoteError) Error() string {
	if line.IsAuthError(e.cause) {
		return fmt.Sprintf("LINE %s requires authentication; run line login", e.Action)
	}
	return fmt.Sprintf("LINE %s failed; check your connection and LINE account settings", e.Action)
}
func (e *RemoteError) Unwrap() error { return e.cause }

// ProtocolError is for classification only. Never log or display its result.
func ProtocolError(err error) error {
	var remote *RemoteError
	if errors.As(err, &remote) {
		return remote.cause
	}
	return err
}

// ReserveSequence writes before the network send and is never rolled back.
// Caller must hold Lock. Keep request IDs in LINE's signed 32-bit range.
func (m *Manager) ReserveSequence() (int64, error) {
	s, err := m.Store.Load()
	if err != nil {
		return 0, err
	}
	if s.Invalidated {
		return 0, errors.New("LINE session was logged out; run line login")
	}
	next := max(m.Now().UnixMilli()%1_000_000_000, s.LastReqSeq+1, 1)
	if next > 2_147_483_647 {
		return 0, errors.New("request sequence exhausted; run line login")
	}
	s.LastReqSeq = next
	if err := m.Store.Save(s); err != nil {
		return 0, err
	}
	return next, nil
}
