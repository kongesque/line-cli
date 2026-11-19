package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	gen "github.com/kongesque/line-cli/pkg"
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
	GetPreviousMessagesV2Context(context.Context, string, line.MessageIDWrapper, int) ([]*line.Message, error)
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
	SendChatCheckedContext(context.Context, int64, string, string) error
	GetAllChatMidsContext(context.Context, bool, bool) (*line.GetAllChatMidsResponse, error)
	AcceptChatInvitationContext(context.Context, int64, string) error
	RejectChatInvitationContext(context.Context, int64, string) error
	UploadOBSWithSID([]byte, string) (string, error)
	UploadOBSWithSIDContext(context.Context, []byte, string) (string, error)
	DetermineMediaMessageFlowContext(context.Context, string) (*line.MediaMessageFlowResponse, error)
	UploadOBSWithOIDAndSIDContext(context.Context, []byte, string, string) error
	UploadOBSPlain([]byte, string, string) error
	UploadOBSPlainContext(context.Context, []byte, string, string) error
	DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error)
}

type Manager struct {
	// Commands also hold the process lock. This serializes callers sharing a manager.
	mu   sync.Mutex
	Wait func(context.Context, time.Duration) error

	// ReadOnly refuses mutations, sequence reservations, and logins even if a
	// command forgets its own gate. Reads, token refresh, and local watch
	// checkpoints remain available.
	ReadOnly bool

	Store      Store
	Storage    StoragePreparer
	NewClient  func(string) API
	ExportKeys func(context.Context, API, *line.LoginResult) (map[string]string, error)
	// CheckSavedKeys confirms that saved Letter Sealing keys hold the account's
	// current key before a certificate login reuses them.
	CheckSavedKeys func(ctx context.Context, api API, mid string, keys map[string]string) error
	Now            func() time.Time
	beginQRKey     func() (qrLoginKey, error)
}

func NewManager(store Store) *Manager {
	manager := &Manager{
		Store: store,
		NewClient: func(token string) API {
			client := line.NewClient(token)
			client.OBSClient.Timeout = 2 * time.Minute
			return client
		},
		ExportKeys:     exportKeys,
		CheckSavedKeys: checkSavedKeys,
		Now:            time.Now,
		beginQRKey:     beginQRKey,
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

// EmailLoginOptions adjusts email login. The zero value sends this device's
// saved certificate when one qualifies (see reusableEmailSession).
type EmailLoginOptions struct {
	// RequirePIN sends no certificate, so LINE asks for phone verification and
	// sends a fresh Letter Sealing keychain.
	RequirePIN bool
}

// LoginContext is EmailLoginContext with default options.
func (m *Manager) LoginContext(ctx context.Context, email, password string, notify Notify) (*line.Profile, error) {
	return m.EmailLoginContext(ctx, email, password, EmailLoginOptions{}, notify)
}

// EmailLoginContext retains the email authentication protocol. Common completion
// is cancellable; the legacy email authentication requests themselves are not
// yet. The caller holds the command lock (see PrepareLogin).
func (m *Manager) EmailLoginContext(ctx context.Context, email, password string, options EmailLoginOptions, notify Notify) (*line.Profile, error) {
	if err := m.readOnly("login"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.PrepareStorage(); err != nil {
		return nil, err
	}
	prior, err := m.reusableEmailSession(email, options)
	if err != nil {
		return nil, err
	}
	certificate, action := "", "login"
	if prior != nil {
		certificate, action = prior.Certificate, "certificate login"
	}
	api := m.NewClient("")
	if cleaner, ok := api.(interface{ ClearLoginKey() error }); ok {
		defer cleaner.ClearLoginKey()
	}
	// LINE may accept a saved certificate without phone verification and then
	// send tokens without a keychain; finishLogin reuses the saved keys only
	// after checking them. A stale certificate gets the usual verification
	// challenge, so there is no separate fallback request (issue #31).
	res, err := api.Login(email, password, certificate)
	if err != nil {
		return nil, remoteError(action, err)
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
			return m.finishLogin(ctx, email, token, noE2EE, res, "email", prior)
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
			// not a reusable certificate until login has completed. The
			// continuation repeats the first request, as Beeper and LINEJS do.
			if err := notify(res.Certificate, true); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res, err = api.Login(email, password, certificate)
		} else {
			return nil, errors.New("LINE login is incomplete; no verification challenge was returned")
		}
		if err != nil {
			return nil, remoteError("phone verification", err)
		}
	}
	return nil, errors.New("LINE login did not complete after phone verification; run line login again")
}

// finishLogin saves the approved session. prior is the saved session whose
// certificate the login sent, or nil; its certificate and keys carry over only
// to the same account.
func (m *Manager) finishLogin(ctx context.Context, email, token string, noE2EE bool, res *line.LoginResult, origin string, prior *State) (profile *line.Profile, err error) {
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
	sameAccount := prior != nil && prior.MID == profile.Mid
	if sameAccount && s.Certificate == "" {
		// LINE need not reissue the certificate it just accepted (Beeper keeps it).
		s.Certificate = prior.Certificate
	}
	if !noE2EE {
		switch {
		case res.E2EEPublicKey != "" && res.EncryptedKeyChain != "":
			s.ExportedKeys, err = m.ExportKeys(ctx, api, res)
			if err != nil {
				return nil, err
			}
		case sameAccount && !prior.NoE2EE && validExportedKeys(prior.ExportedKeys):
			// Without phone verification LINE sends no keychain. Like LINEJS,
			// use saved keys only if they hold the account's current key.
			if err := m.CheckSavedKeys(ctx, api, profile.Mid, prior.ExportedKeys); err != nil {
				return nil, err
			}
			s.ExportedKeys = maps.Clone(prior.ExportedKeys)
		default:
			return nil, errors.New("LINE login returned no Letter Sealing keys; session was not saved; retry line login")
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

// ErrSavedKeysNotCurrent means a certificate login got no keychain and the
// saved Letter Sealing keys do not hold the account's current key.
var ErrSavedKeysNotCurrent = errors.New("saved Letter Sealing keys are not the account's current keys")

// reusableEmailSession returns the saved session whose certificate an email
// login may send: one from an earlier email login for the same address that LINE
// has not logged out, holding the keys a login without phone verification needs.
// Beeper and LINEJS send a saved certificate on email login the same way.
func (m *Manager) reusableEmailSession(email string, options EmailLoginOptions) (*State, error) {
	if options.RequirePIN {
		return nil, nil
	}
	s, err := m.Store.Load()
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("saved session is invalid")
	}
	// An invalidated session's certificate is not reused, as for QR login.
	if s.Invalidated || s.CertificateOrigin != "email" || s.Certificate == "" || s.Email != email || s.MID == "" {
		return nil, nil
	}
	if !s.NoE2EE && !validExportedKeys(s.ExportedKeys) {
		return nil, nil
	}
	return s, nil
}

func validExportedKeys(keys map[string]string) bool {
	for id, key := range keys {
		if id == "" || key == "" {
			return false
		}
	}
	return len(keys) > 0
}

// checkSavedKeys loads saved keys and requires the newest to match the public
// key LINE currently returns for the account, which peers encrypt to. LINEJS
// makes the same check before using stored keys after login. Nothing is
// registered or replaced on failure.
func checkSavedKeys(ctx context.Context, api API, mid string, keys map[string]string) error {
	mgr, err := e2ee.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.LoadMyKeyFromExportedMap(keys); err != nil {
		return err
	}
	rawID, public, err := mgr.MyPublicKey()
	if err != nil {
		return err
	}
	current, err := api.NegotiateE2EEPublicKeyContext(ctx, mid)
	if err != nil {
		return err
	}
	return matchCurrentKey(rawID, public, current)
}

// matchCurrentKey compares the newest saved key with the account's current key.
func matchCurrentKey(rawID int, public string, current *line.E2EEPublicKey) error {
	if current == nil {
		return ErrSavedKeysNotCurrent
	}
	if id, err := current.KeyID.Int64(); err != nil || rawID <= 0 || id != int64(rawID) {
		return ErrSavedKeysNotCurrent
	}
	want, err := gen.NormalizePeerPublicKeyB64(current.PublicKey)
	if err != nil {
		return ErrSavedKeysNotCurrent
	}
	if got, err := gen.NormalizePeerPublicKeyB64(public); err != nil || got != want {
		return ErrSavedKeysNotCurrent
	}
	return nil
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

// DoSquareReadContext runs one allowlisted Square read through the existing
// session lifecycle. The caller chooses only a registered operation and its
// validated arguments; it cannot provide a URL, method, or arbitrary callback.
//
// Thrift application/service exceptions are not assumed to represent auth;
// only HTTP/auth evidence classified by the existing line package participates
// in login invalidation and refresh handling until a verified mapping exists.
func (m *Manager) DoSquareReadContext(ctx context.Context, operation line.SquareReadOperation, arguments []line.SquareField) (*line.SquareReadResponse, error) {
	var response *line.SquareReadResponse
	err := m.do(ctx, func(api API) error {
		provider, ok := api.(interface{ SquareClient() *line.SquareClient })
		if !ok {
			return errors.New("session API does not provide the Square transport")
		}
		var err error
		response, err = provider.SquareClient().CallRead(ctx, operation, arguments)
		return err
	}, true)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Mutate refreshes credentials but never replays the operation, even on auth errors.
// A lost response may mean the server already accepted the mutation.
func (m *Manager) Mutate(call func(API) error) error {
	return m.MutateContext(context.Background(), call)
}

// MutateContext checks cancellation during preparation and never replays a mutation.
func (m *Manager) MutateContext(ctx context.Context, call func(API) error) error {
	return m.mutateContext(ctx, call)
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
	if e.Action == "certificate login" {
		// The cause is unknown; the request is not repeated without the certificate.
		return "LINE login with this device's saved certificate failed; your saved session was not changed. Check your connection and account settings, or run line login --email you@example.com --require-pin to verify with a PIN instead"
	}
	if line.IsAuthError(e.cause) {
		if e.Action == "token refresh" {
			// Retrying the same refresh token does not help (issue #29); only a
			// new login issues usable credentials. The session is not invalidated.
			return "LINE rejected the token refresh; run line login to sign in again"
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
	if err := m.readOnly(""); err != nil {
		return 0, err
	}
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
