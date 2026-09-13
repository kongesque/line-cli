package cli

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
)

// Explicitly enabled only in a disposable VM. LINE is replaced by guidedAPI;
// systemd storage, filesystem locks, CLI selection/status/logout are real.
func TestHeadlessCLIWithNativeSystemdAndFakeLINE(t *testing.T) {
	if os.Getenv("LINE_CLI_TEST_SYSTEMD_CREDS") != "1" {
		t.Skip("requires disposable Linux VM")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, api, out, _, locked := guidedApp(t, "yes\n")
	a.Manager = session.NewManager(session.KeychainStore{})
	a.Manager.NewClient = func(string) session.API { return api }
	a.NewHeadlessLogin = func() (session.LoginStorage, error) { return session.BeginHeadlessLogin(context.Background()) }
	fakeLock := a.Lock
	a.Lock = func() (func(), error) {
		unlock, err := session.Lock()
		if err != nil {
			return nil, err
		}
		release, err := fakeLock()
		if err != nil {
			unlock()
			return nil, err
		}
		return func() { release(); unlock() }, nil
	}
	a.Password = func() (string, error) {
		if *locked {
			t.Fatal("password held lock")
		}
		return "synthetic", nil
	}
	if err := a.Run([]string{"login", "--headless", "--email", "you@example.com"}); err != nil {
		t.Fatal(err)
	}
	if api.logins != 1 {
		t.Fatal("fake LINE login count changed")
	}
	out.Reset()
	if err := a.Run([]string{"auth", "status", "--check", "--json"}); err != nil {
		t.Fatal(err)
	}
	var status session.StorageStatus
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Backend != "headless" || status.Email != "you@example.com" || !status.ProtectionVerified || status.WriteAccess != "available" {
		t.Fatal("incorrect persisted backend status")
	}
	// Ordinary reauthentication preserves the selected backend without a flag.
	if err := a.Run([]string{"login", "--email", "you@example.com"}); err != nil {
		t.Fatal(err)
	}
	if api.logins != 2 {
		t.Fatal("reauthentication failed")
	}
	if err := a.Run([]string{"logout"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Run([]string{"logout"}); err != nil {
		t.Fatal("logout retry failed", err)
	}
	status, err := (session.KeychainStore{}).Status(false)
	if err != nil || status.Configured != "none" {
		t.Fatal("logout left session configured", err)
	}
}
