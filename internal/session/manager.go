package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
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
	GetEncryptedIdentityV3Context(context.Context) (*line.EncryptedIdentityV3, error)
	RefreshAccessToken(string) (*line.TokenV3IssueResult, error)
	GetAllContactIds() ([]string, error)
	GetAllContactIdsContext(context.Context) ([]string, error)
	GetContactsV2([]string) (*line.ContactsResponse, error)
	GetContactsV2Context(context.Context, []string) (*line.ContactsResponse, error)
	GetMessageBoxes(line.MessageBoxesOptions) (*line.MessageBoxesResponse, error)
	GetMessageBoxesContext(context.Context, line.MessageBoxesOptions) (*line.MessageBoxesResponse, error)
	GetRecentMessagesV2(string, int) ([]*line.Message, error)
	GetRecentMessagesV2Context(context.Context, string, int) ([]*line.Message, error)
	GetBlockedContactIds() ([]string, error)
	GetBlockedContactIdsContext(context.Context) ([]string, error)
	NegotiateE2EEPublicKey(string) (*line.E2EEPublicKey, error)
	NegotiateE2EEPublicKeyContext(context.Context, string) (*line.E2EEPublicKey, error)
	GetE2EEPublicKey(string, int, int) (*line.E2EEPublicKey, error)
	GetE2EEPublicKeyContext(context.Context, string, int, int) (*line.E2EEPublicKey, error)
	GetE2EEGroupSharedKey(string, int) (*line.E2EEGroupSharedKey, error)
	GetE2EEGroupSharedKeyContext(context.Context, string, int) (*line.E2EEGroupSharedKey, error)
	GetLastE2EEGroupSharedKey(string) (*line.E2EEGroupSharedKey, error)
	GetLastE2EEGroupSharedKeyContext(context.Context, string) (*line.E2EEGroupSharedKey, error)
	GetChats([]string, bool, bool) (*line.GetChatsResponse, error)
	GetChatsContext(context.Context, []string, bool, bool) (*line.GetChatsResponse, error)
	RegisterE2EEGroupKey(int, string, []string, []int, []string) error
	RegisterE2EEGroupKeyContext(context.Context, int, string, []string, []int, []string) error
	SendMessage(int64, *line.Message) (*line.Message, error)
	SendMessageContext(context.Context, int64, *line.Message) (*line.Message, error)
	React(int64, string, line.ReactionType) error
	ReactContext(context.Context, int64, string, line.ReactionType) error
	CancelReaction(int64, string) error
	CancelReactionContext(context.Context, int64, string) error
	UnsendMessage(int64, string) error
	UnsendMessageContext(context.Context, int64, string) error
	UploadOBSWithSID([]byte, string) (string, error)
	UploadOBSWithSIDContext(context.Context, []byte, string) (string, error)
	UploadOBSPlain([]byte, string, string) error
	UploadOBSPlainContext(context.Context, []byte, string, string) error
	DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error)
}

type Manager struct {
	// Commands also hold the process lock. This serializes callers sharing a manager.
	mu   sync.Mutex
	Wait func(context.Context, time.Duration) error

	Store      Store
	Storage    StoragePreparer
	NewClient  func(string) API
	ExportKeys func(context.Context, API, *line.LoginResult) (map[string]string, error)
	Now        func() time.Time
	beginQRKey func() (qrLoginKey, error)
}

func NewManager(store Store) *Manager {
	manager := &Manager{
		Store: store,
		NewClient: func(token string) API {
			client := line.NewClient(token)
			client.OBSClient.Timeout = 2 * time.Minute
			return client
		},
		ExportKeys: exportKeys,
		Now:        time.Now,
		beginQRKey: beginQRKey,
	}
	if preparer, ok := store.(StoragePreparer); ok {
		manager.Storage = preparer
	}
	return manager
}

// Notify displays the phone PIN; when wait is true it must wait for the user's
// confirmation. Verifier flows poll immediately while the phone prompt is shown.
type Notify func(pin string, wait bool) error

func (m *Manager) PrepareStorage() error {
	if m.Storage != nil {
		return m.Storage.Prepare()
	}
	return nil
}

func (m *Manager) Login(email, password string, notify Notify) (*line.Profile, error) {
	return m.LoginContext(context.Background(), email, password, notify)
}

// LoginContext retains the email authentication protocol. Common completion is
// cancellable; the legacy email authentication requests themselves are not yet.
// The caller holds the command lock (see PrepareLogin).
func (m *Manager) LoginContext(ctx context.Context, email, password string, notify Notify) (*line.Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.PrepareStorage(); err != nil {
		return nil, err
	}
	api := m.NewClient("")
	if cleaner, ok := api.(interface{ ClearLoginKey() error }); ok {
		defer cleaner.ClearLoginKey()
	}
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
			return m.finishLogin(ctx, email, token, noE2EE, res, "email")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if res.Verifier != "" {
			pin := res.PinCode
			if pin == "" {
				pin = res.Pin
			}
			if err := notify(pin, false); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res, err = api.WaitForLogin(res.Verifier, noE2EE)
		} else if res.Certificate != "" {
			// Some LINE responses put a phone challenge in this field; it is
			// not a reusable certificate until login has completed.
			if err := notify(res.Certificate, true); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
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

func (m *Manager) finishLogin(ctx context.Context, email, token string, noE2EE bool, res *line.LoginResult, origin string) (profile *line.Profile, err error) {
	stage := LoginSetup
	defer func() {
		if err != nil {
			err = &LoginError{Stage: stage, Dispatched: true, Approved: true, SaveUncertain: errors.Is(err, ErrDurabilityUncertain), cause: err}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if origin == "qr" {
		if err := validateQRResult(m.Now(), res); err != nil {
			return nil, err
		}
	}
	api := m.NewClient(token)
	profile, err = api.GetProfileContext(ctx)
	if err != nil {
		return nil, err
	}
	if profile == nil || profile.Mid == "" {
		return nil, errors.New("LINE did not return an account ID")
	}
	s := &State{Generation: rand.Text(), Version: 1, AccessToken: token, Certificate: res.Certificate,
		MID: profile.Mid, Email: email, NoE2EE: noE2EE, CertificateOrigin: origin}
	if res.Mid != "" && res.Mid != profile.Mid {
		return nil, errors.New("login profile does not match the approved account")
	}
	if res.TokenV3IssueResult != nil {
		s.applyToken(m.Now(), res.TokenV3IssueResult)
	}
	if !noE2EE {
		if res.E2EEPublicKey == "" || res.EncryptedKeyChain == "" {
			return nil, errors.New("LINE login returned no Letter Sealing keys; session was not saved; retry line login")
		}
		s.ExportedKeys, err = m.ExportKeys(ctx, api, res)
		if err != nil {
			return nil, err
		}
		for id, key := range s.ExportedKeys {
			if id == "" || key == "" {
				return nil, errors.New("incomplete exported Letter Sealing keys")
			}
		}
		if len(s.ExportedKeys) == 0 {
			return nil, errors.New("could not export Letter Sealing keys; session was not saved; retry line login")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stage = LoginSave
	if err := m.Store.Save(s); err != nil {
		return nil, err
	}
	// A completed save wins over cancellation arriving during the commit.
	return profile, nil
}

func exportKeys(ctx context.Context, api API, res *line.LoginResult) (map[string]string, error) {
	mgr, err := e2ee.NewManager()
	if err != nil {
		return nil, err
	}
	identity, err := api.GetEncryptedIdentityV3Context(ctx)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, errors.New("missing encrypted identity")
	}
	if err := mgr.InitStorage(identity.WrappedNonce, identity.KDFParameter1, identity.KDFParameter2); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return mgr.InitFromLoginKeyChain(res.E2EEPublicKey, res.EncryptedKeyChain)
}

// Do is for read-only calls. The caller must hold Lock across the whole command.
func (m *Manager) Do(call func(API) error) error {
	return m.DoContext(context.Background(), call)
}

func (m *Manager) DoContext(ctx context.Context, call func(API) error) error {
	return m.do(ctx, call, true)
}

// Mutate refreshes credentials but never replays the operation, even on auth errors.
// A lost response may mean the server already accepted the mutation.
func (m *Manager) Mutate(call func(API) error) error {
	return m.MutateContext(context.Background(), call)
}

// MutateContext checks cancellation during preparation and never replays a mutation.
func (m *Manager) MutateContext(ctx context.Context, call func(API) error) error {
	return m.do(ctx, call, false)
}

func (m *Manager) do(ctx context.Context, call func(API) error, retry bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := m.Store.Load()
	if err != nil {
		return err
	}
	if s.Invalidated {
		return ErrSessionInvalidated
	}
	api := m.NewClient(s.AccessToken)
	refreshed := false
	if s.RefreshToken != "" && !s.RefreshDeadline().IsZero() && !m.Now().Before(s.RefreshDeadline()) {
		api, err = m.refresh(ctx, s, api)
		if err != nil {
			return err
		}
		refreshed = true
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = call(api)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return remoteError("request", err)
	}
	if line.IsAuthError(err) {
		original := err
		// Source-aware recovery also protects against a response from a stale client.
		current, loadErr := m.Store.Load()
		if loadErr != nil {
			return loadErr
		}
		if current.Generation != s.Generation {
			return ErrSessionChanged
		}
		if current.Invalidated {
			return ErrSessionInvalidated
		}
		stale := current.AccessToken != s.AccessToken || current.RefreshToken != s.RefreshToken
		if !stale && line.IsLoggedOut(err) {
			return m.invalidate(s)
		}
		if stale {
			s = current
			api = m.NewClient(s.AccessToken)
		} else if !refreshed && s.RefreshToken != "" {
			api, err = m.refresh(ctx, s, api)
			if err != nil {
				return err
			}
		} else {
			if s.RefreshToken == "" {
				return remoteError("refresh unavailable", original)
			}
			return remoteError("request", original)
		}
		if !retry {
			return remoteError("mutation (not replayed)", original)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err = call(api)
		if line.IsLoggedOut(err) {
			return m.invalidate(s)
		}
	}
	return remoteError("request", err)
}

func (m *Manager) invalidate(failed *State) error {
	s, err := m.Store.Load()
	if err != nil {
		return err
	}
	if s.Generation != failed.Generation {
		return ErrSessionChanged
	}
	if s.AccessToken != failed.AccessToken || s.RefreshToken != failed.RefreshToken {
		// Never write a stale snapshot over rotated credentials or checkpoints.
		return remoteError("stale client", errors.New("stale authentication response"))
	}
	s.Invalidated = true
	if err := m.Store.Save(s); err != nil {
		return err
	}
	return ErrSessionInvalidated
}

// RecoverStream handles auth errors from an SSE connection under the command lock.
// Stale streams reconnect using stored credentials without refreshing or invalidating.
func (m *Manager) RecoverStream(ctx context.Context, generation, accessToken string, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := m.Store.Load()
	if err != nil {
		return err
	}
	if s.Generation != generation {
		return ErrSessionChanged
	}
	if s.Invalidated {
		return ErrSessionInvalidated
	}
	if s.AccessToken != accessToken {
		return nil
	}
	if line.IsLoggedOut(cause) {
		return m.invalidate(s)
	}
	if !line.IsAuthError(cause) {
		return remoteError("stream", cause)
	}
	if s.RefreshToken == "" {
		return remoteError("refresh unavailable", cause)
	}
	_, err = m.refresh(ctx, s, m.NewClient(s.AccessToken))
	return err
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
	if errors.Is(e.cause, context.Canceled) {
		return "LINE request canceled"
	}
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "LINE command deadline exceeded"
	}
	if line.IsAuthError(e.cause) {
		if e.Action == "token refresh" {
			return "LINE rejected token refresh; saved credentials were retained. Retry later or run line login if rejection persists"
		}
		if e.Action == "mutation (not replayed)" {
			return "LINE mutation was not replayed after an authentication failure; current credentials were saved. Check LINE before trying again"
		}
		if e.Action == "refresh unavailable" {
			return "LINE request requires authentication; no refresh token is stored to recover access; run line login"
		}
		if e.Action == "request" {
			return fmt.Sprintf("LINE %s requires access-token refresh; credentials were retained", e.Action)
		}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.Store.Load()
	if err != nil {
		return 0, err
	}
	if s.Invalidated {
		return 0, ErrSessionInvalidated
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
