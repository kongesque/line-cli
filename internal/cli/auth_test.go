package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
)

type fakeLoginStorage struct {
	initiallyEmptyStore
	consent, accepted, closed bool
	prepares                  int
	err                       error
	identity                  string
}

func (s *fakeLoginStorage) Prepare() error {
	s.prepares++
	if s.err != nil {
		return s.err
	}
	if s.consent && !s.accepted {
		return session.ErrHostConsent
	}
	return nil
}
func (s *fakeLoginStorage) StorageIdentity() (string, error) { return s.identity, nil }
func (s *fakeLoginStorage) RequiresHostConsent() bool        { return s.consent }
func (s *fakeLoginStorage) AcceptHost()                      { s.accepted = true }
func (s *fakeLoginStorage) Close()                           { s.closed = true }

func TestHeadlessLoginConsentFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"yes", "no", "cancel", "storage_failure", "changed"} {
		t.Run(mode, func(t *testing.T) {
			input := "yes\n"
			if mode == "no" {
				input = "no\n"
			}
			if mode == "cancel" {
				input = ""
			}
			a, api, _, diagnostics, locked := guidedApp(t, input)
			store := &fakeLoginStorage{consent: true, identity: "none"}
			if mode == "storage_failure" {
				store.err = session.ErrCredentialHelper
			}
			a.NewHeadlessLogin = func() (session.LoginStorage, error) {
				if !*locked {
					t.Fatal("factory ran unlocked")
				}
				return store, nil
			}
			passwords := 0
			a.Password = func() (string, error) {
				passwords++
				if *locked || !store.accepted {
					t.Fatal("invalid password order")
				}
				if mode == "changed" {
					store.identity = "other-backend"
				}
				return "synthetic", nil
			}
			err := a.Run([]string{"login", "--headless", "--email", "you@example.com"})
			if mode == "yes" {
				if err != nil || api.logins != 1 || store.prepares != 2 {
					t.Fatal(err)
				}
			} else if err == nil || api.logins != 0 {
				t.Fatal("unsafe authentication proceeded", err)
			}
			if mode != "yes" && mode != "changed" && passwords != 0 {
				t.Fatal("password requested before consent/readiness")
			}
			if *locked || !store.closed {
				t.Fatal("leaked lock/candidate")
			}
			if !strings.Contains(diagnostics.String(), "complete disk copy") || !strings.Contains(diagnostics.String(), "no TPM") {
				t.Fatal("missing protection disclosure")
			}
		})
	}
}

type statusTestStore struct {
	testStore
	status session.StorageStatus
	err    error
	calls  int
}

func (s *statusTestStore) Status(check bool) (session.StorageStatus, error) {
	s.calls++
	return s.status, s.err
}
func TestAuthStatusIsLocalAndPreservesFailureJSON(t *testing.T) {
	a, _, out, _, locked := guidedApp(t, "")
	s := &statusTestStore{status: session.StorageStatus{Schema: 1, Configured: "present", Backend: "headless", Protection: "host-user", ReadAccess: "unavailable", WriteAccess: "not_checked", Reboot: "not_verified", LINEValidity: "not_checked", Reason: "storage_unavailable"}, err: session.ErrCredentialHelper}
	a.Manager.Store = s
	a.Manager.NewClient = func(string) session.API { t.Fatal("status contacted LINE"); return nil }
	a.Password = func() (string, error) { t.Fatal("status requested input"); return "", nil }
	a.Interactive = false
	if err := a.Run([]string{"auth", "status", "--json"}); !errors.Is(err, session.ErrCredentialHelper) {
		t.Fatal(err)
	}
	var got session.StorageStatus
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Configured != "present" || got.ProtectionVerified || got.LINEValidity != "not_checked" {
		t.Fatal("status overclaimed verification")
	}
	if s.calls != 1 || *locked {
		t.Fatal("bad status lock lifecycle")
	}
}

func TestHeadlessArgumentsFailBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{{"login", "--headless", "--email", "you@example.com"}, {"auth", "status", "--bad"}, {"auth", "status", "extra"}} {
		a, api, _, _, _ := guidedApp(t, "")
		a.Lock = func() (func(), error) { t.Fatal("invalid or unsupported command accessed storage"); return nil, nil }
		a.Password = func() (string, error) { t.Fatal("invalid command requested a password"); return "", nil }
		if err := a.Run(args); err == nil {
			t.Fatal("invalid command accepted")
		}
		if api.logins != 0 {
			t.Fatal("invalid command authenticated")
		}
	}
}

func TestMigrationConsentAndLocalLocking(t *testing.T) {
	for _, answer := range []string{"yes\n", "no\n", ""} {
		a, _, out, _, locked := guidedApp(t, answer)
		a.Manager.NewClient = func(string) session.API { t.Fatal("migration contacted LINE"); return nil }
		a.Password = func() (string, error) { t.Fatal("migration requested password"); return "", nil }
		calls := 0
		a.MigrateHeadless = func(accepted bool) error {
			calls++
			if !accepted || !*locked {
				t.Fatal("migration without consent/lock")
			}
			return nil
		}
		err := a.Run([]string{"auth", "migrate", "--storage=headless"})
		if answer == "yes\n" {
			if err != nil || calls != 1 || !strings.Contains(out.String(), "Migration and native-key cleanup complete") {
				t.Fatal(err)
			}
		} else if err == nil || calls != 0 {
			t.Fatal("cancelled migration ran", err)
		}
		if *locked {
			t.Fatal("migration leaked lock")
		}
	}
}

func TestMigrationValidationAndCleanupFailure(t *testing.T) {
	for _, args := range [][]string{{"auth", "migrate"}, {"auth", "migrate", "--storage=native"}, {"auth", "migrate", "--storage=headless", "extra"}, {"auth", "migrate", "--storage=headless"}} {
		a, _, _, _, _ := guidedApp(t, "")
		a.Lock = func() (func(), error) { t.Fatal("invalid/unsupported migration touched storage"); return nil, nil }
		if err := a.Run(args); err == nil {
			t.Fatal("invalid migration accepted")
		}
	}
	a, _, out, _, locked := guidedApp(t, "yes\n")
	a.MigrateHeadless = func(bool) error { return session.ErrMigrationPending }
	if err := a.Run([]string{"auth", "migrate", "--storage=headless"}); !errors.Is(err, session.ErrMigrationPending) {
		t.Fatal(err)
	}
	if *locked || strings.Contains(out.String(), "complete") {
		t.Fatal("incomplete migration reported success or leaked lock")
	}
}
