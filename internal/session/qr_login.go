package session

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"time"

	runner "github.com/kongesque/line-cli/pkg"
	"github.com/kongesque/line-cli/pkg/line"
)

const QRLoginMaxAttempts = 3

type QRLoginEventKind string

const (
	QRLoginCode          QRLoginEventKind = "code"
	QRLoginExpired       QRLoginEventKind = "expired"
	QRLoginScanned       QRLoginEventKind = "scanned"
	QRLoginPIN           QRLoginEventKind = "pin"
	QRLoginPhoneAccepted QRLoginEventKind = "phone_accepted"
	QRLoginApproved      QRLoginEventKind = "approved"
)

// QRLoginEvent contains ephemeral secrets. Consumers may display URL/PIN only
// as requested by the user; never log, persist, or include them in errors.
// ExpiresIn is an estimate including scan-poll backoff, never an expiry signal.
type QRLoginEvent struct {
	Kind      QRLoginEventKind
	URL       string
	PIN       string
	Attempt   int
	ExpiresIn time.Duration
}

type qrAPI interface {
	BeginQRLogin(context.Context) (*line.QRChallenge, error)
	WaitForQRScan(context.Context, *line.QRChallenge) error
	VerifyQRCertificate(context.Context, string, string) error
	CreateQRPin(context.Context, string) (string, error)
	WaitForQRPin(context.Context, string) error
	CompleteQRLogin(context.Context, string) (*line.LoginResult, error)
}

type qrLoginKey interface {
	Generate() (string, error)
	Close() error
}

func beginQRKey() (qrLoginKey, error) {
	r, err := runner.GetRunner()
	if err != nil {
		return nil, err
	}
	return r.BeginQRLoginKey()
}

// LoginQR owns fresh-session retries and the login-key lease through export and
// save. The caller holds the command lock, after PrepareLogin/CheckLogin around
// local prompts. Event callbacks display progress and must honor ctx; they must
// not release the lock or request saved-session replacement confirmation.
func (m *Manager) LoginQR(ctx context.Context, notify func(QRLoginEvent) error) (profile *line.Profile, err error) {
	stage := LoginLocal
	dispatched, approved := false, false
	defer func() {
		var outcome *LoginError
		if err != nil && !errors.As(err, &outcome) {
			err = &LoginError{Stage: stage, Dispatched: dispatched, Approved: approved, cause: err}
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = m.PrepareStorage(); err != nil {
		return nil, err
	}
	if notify == nil {
		return nil, errors.New("QR login requires an event handler")
	}
	certificate := ""
	s, loadErr := m.Store.Load()
	if loadErr != nil && !errors.Is(loadErr, ErrNotFound) {
		return nil, loadErr
	}
	if loadErr == nil {
		if s == nil {
			return nil, errors.New("saved session is invalid")
		}
		if !s.Invalidated && s.CertificateOrigin == "qr" {
			certificate = s.Certificate
		}
	}
	api, ok := m.NewClient("").(qrAPI)
	if !ok {
		return nil, errors.New("QR login is unavailable")
	}
	key, err := m.beginQRKey()
	if err != nil {
		return nil, err
	}
	// Close is always attempted, including display, crypto, and storage failures.
	// After a committed save, cleanup cannot turn success into an unsaved claim.
	defer key.Close()
	emit := func(event QRLoginEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := notify(event); err != nil {
			return err
		}
		return ctx.Err()
	}
	stage = LoginBeforeScan
	for attempt := 1; attempt <= QRLoginMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		challenge, err := api.BeginQRLogin(ctx)
		if err != nil {
			return nil, err
		}
		publicKey, err := key.Generate()
		if err != nil {
			return nil, err
		}
		qrURL, expires, err := qrLoginURL(challenge, publicKey)
		if err != nil {
			return nil, err
		}
		if err := emit(QRLoginEvent{Kind: QRLoginCode, URL: qrURL, Attempt: attempt, ExpiresIn: expires}); err != nil {
			return nil, err
		}
		if err := api.WaitForQRScan(ctx, challenge); err != nil {
			if !errors.Is(err, line.ErrQRCodeExpired) {
				return nil, err
			}
			if err := emit(QRLoginEvent{Kind: QRLoginExpired, Attempt: attempt}); err != nil {
				return nil, err
			}
			if attempt == QRLoginMaxAttempts {
				return nil, line.ErrQRCodeExpired
			}
			continue
		}
		stage = LoginPhoneApproval
		if err := emit(QRLoginEvent{Kind: QRLoginScanned, Attempt: attempt}); err != nil {
			return nil, err
		}
		if err := api.VerifyQRCertificate(ctx, challenge.AuthSessionID, certificate); err != nil {
			// No service code is currently mapped to this sentinel. Only an
			// evidenced certificate rejection may enable the PIN branch.
			if !errors.Is(err, line.ErrQRCertificateRejected) {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pin, err := api.CreateQRPin(ctx, challenge.AuthSessionID)
			if err != nil {
				return nil, err
			}
			if pin == "" {
				return nil, errors.New("missing phone verification code")
			}
			if err := emit(QRLoginEvent{Kind: QRLoginPIN, PIN: pin, Attempt: attempt}); err != nil {
				return nil, err
			}
			if err := api.WaitForQRPin(ctx, challenge.AuthSessionID); err != nil {
				return nil, err
			}
		}
		if err := emit(QRLoginEvent{Kind: QRLoginPhoneAccepted, Attempt: attempt}); err != nil {
			return nil, err
		}
		stage = LoginFinal
		dispatched = true // Conservative for implementations lacking dispatch detail.
		res, err := api.CompleteQRLogin(ctx, challenge.AuthSessionID)
		if err != nil {
			var completion *line.QRCompleteError
			if errors.As(err, &completion) {
				dispatched, approved = completion.Dispatched, completion.Approved
			}
			return nil, err
		}
		approved = res != nil && res.TokenV3IssueResult != nil && res.TokenV3IssueResult.AccessToken != ""
		if !approved {
			return nil, errors.New("missing completed login")
		}
		stage = LoginSetup
		if err := emit(QRLoginEvent{Kind: QRLoginApproved, Attempt: attempt}); err != nil {
			return nil, err
		}
		return m.finishLogin(ctx, "", res.TokenV3IssueResult.AccessToken, false, res, "qr")
	}
	return nil, line.ErrQRCodeExpired
}

func qrLoginURL(challenge *line.QRChallenge, publicKey string) (string, time.Duration, error) {
	if challenge == nil || challenge.AuthSessionID == "" || challenge.LongPollIntervalSec <= 0 || challenge.LongPollIntervalSec > 3600 || challenge.LongPollMaxCount <= 0 || challenge.LongPollMaxCount > 1000 {
		return "", 0, errors.New("invalid QR challenge")
	}
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(pub) != 32 {
		return "", 0, errors.New("invalid QR public key")
	}
	u, err := url.Parse(challenge.CallbackURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", 0, errors.New("invalid QR callback")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", 0, errors.New("invalid QR callback query")
	}
	if query.Has("secret") || query.Has("e2eeVersion") {
		return "", 0, errors.New("QR callback already contains login key parameters")
	}
	// Chrome appends these fields. Preserve the server's original query bytes,
	// which can contain opaque callback data, instead of re-encoding them.
	if u.RawQuery != "" {
		u.RawQuery += "&"
	}
	u.RawQuery += "secret=" + url.QueryEscape(publicKey) + "&e2eeVersion=1"
	estimate := time.Duration(challenge.LongPollIntervalSec) * time.Second * time.Duration(challenge.LongPollMaxCount)
	delay := time.Second
	for poll := 1; poll < challenge.LongPollMaxCount; poll++ {
		estimate += delay
		delay = min(delay*2, 30*time.Second)
	}
	return u.String(), estimate, nil
}

func validateQRResult(now time.Time, res *line.LoginResult) error {
	// Retain the existing token V3 decimal-string wire contract. Other wire
	// types remain unverified and are rejected by pkg/line's JSON decoder.
	if res == nil || res.TokenV3IssueResult == nil || res.TokenV3IssueResult.AccessToken == "" || res.TokenV3IssueResult.RefreshToken == "" || refreshAt(now, res.TokenV3IssueResult).IsZero() {
		return errors.New("QR login returned incomplete refresh data")
	}
	for _, digit := range res.TokenV3IssueResult.DurationUntilRefreshSec {
		if digit < '0' || digit > '9' {
			return errors.New("QR login returned invalid refresh duration")
		}
	}
	if res.NoE2EE || res.Certificate == "" || res.E2EEKeyID == "" {
		return errors.New("QR login returned incomplete encryption data")
	}
	pub, pubErr := base64.StdEncoding.DecodeString(res.E2EEPublicKey)
	chain, chainErr := base64.StdEncoding.DecodeString(res.EncryptedKeyChain)
	if pubErr != nil || len(pub) < 32 || chainErr != nil || len(chain) == 0 {
		return errors.New("QR login returned invalid encryption data")
	}
	return nil
}
