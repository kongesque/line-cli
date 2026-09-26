package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

type testQRKey struct{ generated, closed int }

func (k *testQRKey) Generate() (string, error) {
	k.generated++
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(byte(k.generated)), 32))), nil
}
func (k *testQRKey) Close() error { k.closed++; return nil }

type fakeQRAPI struct {
	pinWait  func(context.Context) error
	complete func(context.Context) (*line.LoginResult, error)
	API
	calls                                      []string
	certificates                               []string
	result                                     *line.LoginResult
	scan                                       func(context.Context) error
	verifyErr, pinErr, completeErr, profileErr error
	profile                                    func(context.Context) (*line.Profile, error)
	beginCount, completeCount                  int
}

func (f *fakeQRAPI) BeginQRLogin(context.Context) (*line.QRChallenge, error) {
	f.calls = append(f.calls, "begin")
	f.beginCount++
	return &line.QRChallenge{AuthSessionID: fmt.Sprintf("synthetic-%d", f.beginCount), CallbackURL: "https://example.test/qr?session=synthetic&opaque=%2f%20", LongPollIntervalSec: 2, LongPollMaxCount: 3}, nil
}
func (f *fakeQRAPI) WaitForQRScan(ctx context.Context, _ *line.QRChallenge) error {
	f.calls = append(f.calls, "scan")
	if f.scan != nil {
		return f.scan(ctx)
	}
	return nil
}
func (f *fakeQRAPI) VerifyQRCertificate(_ context.Context, _, certificate string) error {
	f.calls = append(f.calls, "certificate")
	f.certificates = append(f.certificates, certificate)
	return f.verifyErr
}
func (f *fakeQRAPI) CreateQRPin(context.Context, string) (string, error) {
	f.calls = append(f.calls, "pin")
	return "001234", nil
}
func (f *fakeQRAPI) WaitForQRPin(ctx context.Context, _ string) error {
	f.calls = append(f.calls, "approval")
	if f.pinWait != nil {
		return f.pinWait(ctx)
	}
	if f.pinErr != nil {
		return f.pinErr
	}
	return ctx.Err()
}
func (f *fakeQRAPI) CompleteQRLogin(ctx context.Context, _ string) (*line.LoginResult, error) {
	f.calls = append(f.calls, "complete")
	f.completeCount++
	if f.complete != nil {
		return f.complete(ctx)
	}
	return f.result, f.completeErr
}
func (f *fakeQRAPI) GetProfileContext(ctx context.Context) (*line.Profile, error) {
	f.calls = append(f.calls, "profile")
	if f.profile != nil {
		return f.profile(ctx)
	}
	return &line.Profile{Mid: "synthetic-mid", DisplayName: "Synthetic"}, f.profileErr
}

func qrTestManager(t *testing.T) (*Manager, *memoryStore, *fakeQRAPI, *testQRKey) {
	t.Helper()
	s := &memoryStore{state: &State{AccessToken: "old", Generation: "old-generation", MID: "old-mid", Certificate: "old-cert", Email: "old@example.test"}}
	f := &fakeQRAPI{result: &line.LoginResult{Certificate: "qr-cert", Mid: "synthetic-mid", E2EEKeyID: "123", E2EEPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)), EncryptedKeyChain: base64.StdEncoding.EncodeToString([]byte("synthetic-chain")), TokenV3IssueResult: &line.TokenV3IssueResult{AccessToken: "new", RefreshToken: "refresh", DurationUntilRefreshSec: "3600"}}}
	key := &testQRKey{}
	m := NewManager(s)
	m.NewClient = func(string) API { return f }
	m.beginQRKey = func() (qrLoginKey, error) { return key, nil }
	m.Now = func() time.Time { return time.Unix(1000, 0) }
	m.ExportKeys = func(ctx context.Context, _ API, _ *line.LoginResult) (map[string]string, error) {
		if key.closed != 0 || key.generated == 0 {
			t.Fatal("key lease ended before export")
		}
		f.calls = append(f.calls, "export")
		return map[string]string{"123": "synthetic-export"}, ctx.Err()
	}
	return m, s, f, key
}

func ignoreQREvent(QRLoginEvent) error { return nil }

func TestQRLoginPINOrchestration(t *testing.T) {
	m, s, f, key := qrTestManager(t)
	// Exercise the state-machine contract only. This sentinel deliberately has
	// no server code mapping; this is not evidence of a LINE rejection code.
	f.verifyErr = line.ErrQRCertificateRejected
	var events []QRLoginEventKind
	_, err := m.LoginQR(context.Background(), func(event QRLoginEvent) error {
		events = append(events, event.Kind)
		if event.Kind == QRLoginCode {
			u, err := url.Parse(event.URL)
			if err != nil || u.Query().Get("session") != "synthetic" || !strings.HasPrefix(u.RawQuery, "session=synthetic&opaque=%2f%20&secret=") || len(u.Query()["secret"]) != 1 || u.Query().Get("e2eeVersion") != "1" || event.ExpiresIn != 9*time.Second {
				t.Fatal("invalid QR presentation data")
			}
		}
		if event.Kind == QRLoginPIN && event.PIN != "001234" {
			t.Fatal("PIN changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"begin", "scan", "certificate", "pin", "approval", "complete", "profile", "export"}) {
		t.Fatal(f.calls)
	}
	if !reflect.DeepEqual(events, []QRLoginEventKind{QRLoginCode, QRLoginScanned, QRLoginPIN, QRLoginPhoneAccepted, QRLoginApproved}) {
		t.Fatal(events)
	}
	if s.saves != 1 || s.state.Email != "" || s.state.NoE2EE || s.state.CertificateOrigin != "qr" || s.state.Certificate != "qr-cert" || s.state.AccessToken != "new" || s.state.RefreshToken != "refresh" || !s.state.RefreshAt.Equal(time.Unix(4570, 0)) || s.state.MID != "synthetic-mid" || s.state.ExportedKeys["123"] == "" || s.state.Generation == "old-generation" || key.closed != 1 {
		t.Fatal("incomplete session or key cleanup")
	}
}

func TestQRCertificateOrigins(t *testing.T) {
	for _, origin := range []string{"", "email", "qr", "invalidated"} {
		t.Run(origin, func(t *testing.T) {
			m, s, f, _ := qrTestManager(t)
			s.state.CertificateOrigin = origin
			if origin == "invalidated" {
				s.state.CertificateOrigin, s.state.Invalidated = "qr", true
			}
			_, err := m.LoginQR(context.Background(), ignoreQREvent)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if origin == "qr" {
				want = "old-cert"
			}
			if len(f.certificates) != 1 || f.certificates[0] != want || strings.Contains(strings.Join(f.calls, ","), "pin") {
				t.Fatal("unsafe certificate reuse or unexpected PIN")
			}
		})
	}
}

func TestQRExpiryFreshAttemptsOnlyBeforeScan(t *testing.T) {
	for _, scenario := range []string{"retry", "exhausted", "network", "deadline", "pin_timeout", "unknown_certificate"} {
		t.Run(scenario, func(t *testing.T) {
			m, s, f, key := qrTestManager(t)
			f.scan = func(context.Context) error {
				switch scenario {
				case "retry":
					if f.beginCount == 1 {
						return line.ErrQRCodeExpired
					}
				case "exhausted":
					return line.ErrQRCodeExpired
				case "network":
					return errors.New("secret network failure")
				case "deadline":
					return context.DeadlineExceeded
				}
				return nil
			}
			if scenario == "pin_timeout" {
				f.verifyErr, f.pinErr = line.ErrQRCertificateRejected, line.ErrQRPinTimeout
			}
			if scenario == "unknown_certificate" {
				f.verifyErr = &line.QRServiceError{Method: "verifyCertificate", Code: 500}
			}
			var urls []string
			_, err := m.LoginQR(context.Background(), func(event QRLoginEvent) error {
				if event.Kind == QRLoginCode {
					urls = append(urls, event.URL)
				}
				return nil
			})
			wantAttempts := 1
			if scenario == "retry" {
				wantAttempts = 2
				if err != nil || s.saves != 1 {
					t.Fatal(err)
				}
			} else if err == nil || s.saves != 0 || s.state.AccessToken != "old" {
				t.Fatal("unexpected save")
			}
			if scenario == "exhausted" {
				wantAttempts = 3
				if !errors.Is(err, line.ErrQRCodeExpired) {
					t.Fatal(err)
				}
			}
			if f.beginCount != wantAttempts || key.generated != wantAttempts || key.closed != 1 {
				t.Fatal("wrong regeneration/cleanup count")
			}
			if len(urls) > 1 && urls[0] == urls[1] {
				t.Fatal("regeneration reused curve key")
			}
			if scenario == "unknown_certificate" && strings.Contains(strings.Join(f.calls, ","), "pin") {
				t.Fatal("unknown error enabled fallback")
			}
		})
	}
}

func TestQRIncompleteResultsNeverSave(t *testing.T) {
	cases := map[string]func(*line.LoginResult){
		"no_refresh":        func(r *line.LoginResult) { r.TokenV3IssueResult.RefreshToken = "" },
		"no_token":          func(r *line.LoginResult) { r.TokenV3IssueResult.AccessToken = "" },
		"no_v3":             func(r *line.LoginResult) { r.TokenV3IssueResult = nil },
		"no_public_key":     func(r *line.LoginResult) { r.E2EEPublicKey = "" },
		"malformed_chain":   func(r *line.LoginResult) { r.EncryptedKeyChain = "!" },
		"no_key_id":         func(r *line.LoginResult) { r.E2EEKeyID = "" },
		"no_certificate":    func(r *line.LoginResult) { r.Certificate = "" },
		"no_e2ee":           func(r *line.LoginResult) { r.NoE2EE = true },
		"different_profile": func(r *line.LoginResult) { r.Mid = "different" },
	}
	for _, duration := range []string{"", "0", "-1", "+1", " 1", "1.5", "1e3", "31536001", "9223372036854775808"} {
		cases["duration_"+duration] = func(r *line.LoginResult) { r.TokenV3IssueResult.DurationUntilRefreshSec = duration }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, s, f, key := qrTestManager(t)
			mutate(f.result)
			_, err := m.LoginQR(context.Background(), ignoreQREvent)
			if err == nil || s.saves != 0 || s.state.AccessToken != "old" || f.completeCount != 1 || key.closed != 1 {
				t.Fatal("incomplete login saved or retried")
			}
		})
	}
}

func TestQRFinalAndSetupErrors(t *testing.T) {
	for _, scenario := range []string{"lost_response", "known_not_dispatched", "approved_metadata_failure", "profile", "export", "empty_export", "save"} {
		t.Run(scenario, func(t *testing.T) {
			m, s, f, key := qrTestManager(t)
			sensitive := errors.New("SENSITIVE-server-body-token")
			switch scenario {
			case "lost_response":
				f.completeErr = sensitive
			case "known_not_dispatched":
				f.completeErr = &line.QRCompleteError{}
			case "approved_metadata_failure":
				f.completeErr = &line.QRCompleteError{Dispatched: true, Approved: true}
			case "profile":
				f.profileErr = sensitive
			case "export":
				m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) { return nil, sensitive }
			case "empty_export":
				m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) { return nil, nil }
			case "save":
				s.saveErr = sensitive
			}
			_, err := m.LoginQR(context.Background(), ignoreQREvent)
			var outcome *LoginError
			if !errors.As(err, &outcome) || strings.Contains(err.Error(), "SENSITIVE") || s.saves != 0 || s.state.AccessToken != "old" || f.completeCount != 1 || key.closed != 1 {
				t.Fatal("unsafe failure", err)
			}
			if outcome.Dispatched != (scenario != "known_not_dispatched") {
				t.Fatal("incorrect dispatch outcome")
			}
			if outcome.Approved != (scenario != "known_not_dispatched" && scenario != "lost_response") {
				t.Fatal("incorrect approval outcome")
			}
		})
	}
}

func TestQRCancellationBeforeSave(t *testing.T) {
	for _, point := range []string{"scan", "pin", "phone_accepted", "approved", "profile", "export"} {
		t.Run(point, func(t *testing.T) {
			m, s, f, key := qrTestManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if point == "scan" {
				f.scan = func(ctx context.Context) error { cancel(); return ctx.Err() }
			}
			if point == "pin" {
				f.verifyErr = line.ErrQRCertificateRejected
			}
			if point == "profile" {
				f.profile = func(ctx context.Context) (*line.Profile, error) { cancel(); return nil, ctx.Err() }
			}
			if point == "export" {
				m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) {
					cancel()
					return map[string]string{"key": "value"}, nil
				}
			}
			_, err := m.LoginQR(ctx, func(event QRLoginEvent) error {
				if string(event.Kind) == point {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || s.saves != 0 || s.state.AccessToken != "old" || key.closed != 1 {
				t.Fatal("cancellation saved or lost its identity", err)
			}
			if (point == "scan" || point == "pin" || point == "phone_accepted") && f.completeCount != 0 {
				t.Fatal("final dispatched after cancellation")
			}
		})
	}
}

type commitStore struct {
	*memoryStore
	cancel    context.CancelFunc
	uncertain bool
}

func (s commitStore) Save(state *State) error {
	if err := s.memoryStore.Save(state); err != nil {
		return err
	}
	s.cancel()
	if s.uncertain {
		return ErrDurabilityUncertain
	}
	return nil
}
func TestQRSaveOutcomeWinsOverLateCancellation(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			m, s, _, key := qrTestManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.Store = commitStore{s, cancel, uncertain}
			profile, err := m.LoginQR(ctx, ignoreQREvent)
			if s.saves != 1 || s.state.AccessToken != "new" || key.closed != 1 {
				t.Fatal("commit lost")
			}
			if uncertain {
				var outcome *LoginError
				if !errors.As(err, &outcome) || !outcome.SaveUncertain || !errors.Is(err, ErrDurabilityUncertain) || strings.Contains(err.Error(), "not changed") {
					t.Fatal("incorrect uncertain-save report", err)
				}
			} else if err != nil || profile == nil {
				t.Fatal("late cancellation hid successful commit", err)
			}
		})
	}
}

func TestQRPreflightAndDisplayFailure(t *testing.T) {
	m, _, f, key := qrTestManager(t)
	m.Storage = failingPreparer{}
	_, err := m.LoginQR(context.Background(), ignoreQREvent)
	if !errors.Is(err, ErrStorageCleanup) || len(f.calls) != 0 || key.generated != 0 {
		t.Fatal("remote login before preflight")
	}
	m.Storage = nil
	_, err = m.LoginQR(context.Background(), func(QRLoginEvent) error { return errors.New("SENSITIVE-url") })
	if err == nil || strings.Contains(err.Error(), "SENSITIVE") || len(f.calls) != 1 || key.closed != 1 {
		t.Fatal("display failure did not stop login")
	}
}

type failingPreparer struct{}

func (failingPreparer) Prepare() error { return ErrStorageCleanup }

func TestQRBlockedRequestsObserveCancellation(t *testing.T) {
	for _, point := range []string{"scan", "pin", "complete", "profile", "export"} {
		t.Run(point, func(t *testing.T) {
			m, s, f, key := qrTestManager(t)
			started := make(chan struct{})
			wait := func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
			switch point {
			case "scan":
				f.scan = wait
			case "pin":
				f.verifyErr, f.pinWait = line.ErrQRCertificateRejected, wait
			case "complete":
				f.complete = func(ctx context.Context) (*line.LoginResult, error) { return nil, wait(ctx) }
			case "profile":
				f.profile = func(ctx context.Context) (*line.Profile, error) { return nil, wait(ctx) }
			case "export":
				m.ExportKeys = func(ctx context.Context, _ API, _ *line.LoginResult) (map[string]string, error) {
					return nil, wait(ctx)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := m.LoginQR(ctx, ignoreQREvent); result <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-result:
				var outcome *LoginError
				if !errors.Is(err, context.Canceled) || !errors.As(err, &outcome) || s.saves != 0 || key.closed != 1 || f.beginCount != 1 {
					t.Fatal("cancellation not propagated", err)
				}
				if outcome.Dispatched != (point == "complete" || point == "profile" || point == "export") || outcome.Approved != (point == "profile" || point == "export") {
					t.Fatal("wrong cancellation outcome")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop login")
			}
		})
	}
}

func TestQRURLRejectsPreexistingKeyParameters(t *testing.T) {
	for _, query := range []string{"secret=stale", "e2eeVersion=1"} {
		challenge := &line.QRChallenge{AuthSessionID: "synthetic", CallbackURL: "https://example.test/qr?" + query, LongPollIntervalSec: 1, LongPollMaxCount: 1}
		if _, _, err := qrLoginURL(challenge, base64.StdEncoding.EncodeToString(make([]byte, 32))); err == nil {
			t.Fatal("ambiguous login key parameters accepted")
		}
	}
}
