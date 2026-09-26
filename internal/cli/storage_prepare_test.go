package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
)

type testPreparer func() error

func (p testPreparer) Prepare() error { return p() }

func TestLoginStorageFailurePrecedesPasswordAndNetwork(t *testing.T) {
	a, api, _, _, locked := guidedApp(t, "")
	a.Manager.Storage = testPreparer(func() error {
		if !*locked {
			t.Fatal("preflight without session lock")
		}
		return session.ErrStorageCleanup
	})
	a.Password = func() (string, error) { t.Fatal("storage failure requested a password"); return "", nil }
	if err := a.Run([]string{"login", "--email", "synthetic@example.test"}); !errors.Is(err, session.ErrStorageCleanup) {
		t.Fatal(err)
	}
	if api.logins != 0 || *locked {
		t.Fatal("network call or leaked lock after failed preflight")
	}
}

func TestLoginPreflightRechecksAfterPassword(t *testing.T) {
	for _, scenario := range []string{"success", "storage_failure", "account_change", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			a, api, _, _, locked := guidedApp(t, "")
			checks := 0
			a.Manager.Storage = testPreparer(func() error {
				if !*locked {
					t.Fatal("preflight without session lock")
				}
				checks++
				if checks == 2 && scenario == "storage_failure" {
					return session.ErrStorageCleanup
				}
				return nil
			})
			a.Password = func() (string, error) {
				if *locked || checks != 1 {
					t.Fatal("password lock/order violation")
				}
				if scenario == "account_change" {
					a.Manager.Store.(*testStore).state.Generation = "concurrent-login"
				}
				if scenario == "cancel" {
					return "", ErrCancelled
				}
				return "synthetic", nil
			}
			err := a.Run([]string{"login", "--email", "you@example.com", "--force"})
			if scenario == "success" {
				if err != nil || api.logins != 1 || checks != 2 {
					t.Fatal("login not gated correctly", err)
				}
			} else if err == nil || api.logins != 0 {
				t.Fatal("unsafe login proceeded", err)
			}
			if *locked {
				t.Fatal("session lock leaked")
			}
		})
	}
}

type initiallyEmptyStore struct{ testStore }

func (s *initiallyEmptyStore) Load() (*session.State, error) {
	if s.state == nil {
		return nil, session.ErrNotFound
	}
	return s.testStore.Load()
}

func TestLoginRejectsConcurrentSessionCreation(t *testing.T) {
	a, api, _, _, locked := guidedApp(t, "")
	store := &initiallyEmptyStore{}
	a.Manager.Store = store
	a.Manager.Storage = testPreparer(func() error { return nil })
	a.Password = func() (string, error) {
		store.state = &session.State{Version: 1, MID: "other", AccessToken: "synthetic", Generation: "new"}
		return "synthetic", nil
	}
	if err := a.Run([]string{"login", "--email", "you@example.com", "--force"}); err == nil {
		t.Fatal("concurrent session creation was ignored")
	}
	if api.logins != 0 || *locked {
		t.Fatal("remote login or leaked lock after concurrent session creation")
	}
}

func TestLoginSnapshotPrecedesAllLocalInputWithoutPreparer(t *testing.T) {
	for _, point := range []string{"password", "confirmation"} {
		t.Run(point, func(t *testing.T) {
			a, api, _, _, locked := guidedApp(t, "y\nyou@example.com\n")
			store := &initiallyEmptyStore{}
			if point == "confirmation" {
				store.state = &session.State{MID: "old", AccessToken: "old", Generation: "old"}
			}
			a.Manager.Store, a.Manager.Storage = store, nil
			change := func() { store.state = &session.State{MID: "new", AccessToken: "new", Generation: "new"} }
			input := "you@example.com\n"
			if point == "confirmation" {
				input = "y\nyou@example.com\n"
			}
			a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader(input), onRead: func() {
				if point != "password" {
					change()
				}
			}}
			a.Password = func() (string, error) {
				if *locked {
					t.Fatal("password held lock")
				}
				if point == "password" {
					change()
				}
				return "synthetic", nil
			}
			if err := a.Run([]string{"login", "--email", "you@example.com"}); err == nil || api.logins != 0 || *locked {
				t.Fatal("concurrent session change ignored or lock leaked", err)
			}
		})
	}
}
