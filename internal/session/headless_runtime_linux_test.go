package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A persistent synthetic fixture for disposable VM boot/cron/broker checks.
// The explicit directory and mode gates keep ordinary tests nonpersistent.
// No LINE API is constructed and no personal config/keyring is accessed.
func TestLinuxHeadlessPersistentFixture(t *testing.T) {
	dir := os.Getenv("LINE_CLI_TEST_HEADLESS_FIXTURE")
	mode := os.Getenv("LINE_CLI_TEST_HEADLESS_MODE")
	if os.Getenv("LINE_CLI_TEST_SYSTEMD_CREDS") != "1" || dir == "" {
		t.Skip("requires explicit disposable VM fixture")
	}
	if !filepath.IsAbs(dir) || (mode != "enroll" && mode != "verify" && mode != "denied") {
		t.Fatal("invalid fixture arguments")
	}
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "synthetic.boot")
	if mode == "enroll" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal("fixture must be fresh", err)
		}
		_, err = f.Write(boot)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal("fixture marker could not be saved")
		}
	} else {
		original, err := os.ReadFile(marker)
		if err != nil || len(original) != len(boot) {
			t.Fatal("missing synthetic fixture marker")
		}
		if os.Getenv("LINE_CLI_TEST_EXPECT_REBOOT") == "1" && bytes.Equal(original, boot) {
			t.Fatal("fixture was not rebooted")
		}
	}
	u, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer u()
	s, err := resolvedLinuxStorage()
	if err != nil {
		t.Fatal(err)
	}
	want := envelopeState()
	if mode == "enroll" {
		if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
			t.Fatal("refusing to overwrite an existing fixture session")
		}
		p, err := s.beginLogin()
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		p.AcceptHost()
		if err := p.Save(want); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load()
	if mode == "denied" {
		if !errors.Is(err, ErrCredentialHelper) && !errors.Is(err, ErrHeadlessUnavailable) {
			t.Fatal("expected broker rejection, not success, timeout, or filesystem denial", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if got.LastReqSeq < want.LastReqSeq {
		t.Fatal("sequence rolled back")
	}
	want.LastReqSeq = got.LastReqSeq
	if !sameStoredState(want, got) {
		t.Fatal("persistent fixture state changed")
	}
	m := NewManager(s)
	m.Now = func() time.Time { return time.UnixMilli(1) }
	if seq, err := m.ReserveSequence(); err != nil || seq != want.LastReqSeq+1 {
		t.Fatal("fixture sequence was not saved", err)
	}
	status, err := s.Status(true)
	if err != nil || status.Backend != "headless" || !status.ProtectionVerified || status.WriteAccess != "available" {
		t.Fatal("fixture readiness failed", err)
	}
	// A second full load verifies the persisted update, including all saved keys.
	got, err = s.Load()
	want.LastReqSeq++
	if err != nil || !sameStoredState(want, got) {
		t.Fatal("fixture write failed", err)
	}
}
