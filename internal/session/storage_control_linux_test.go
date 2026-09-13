package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

func TestStatusReportsStoragePathFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", path)
	status, err := (KeychainStore{}).Status(false)
	if err == nil || status.Reason == "ok" || status.Configured == "none" {
		t.Fatal("invalid storage path reported as healthy or logged out", status, err)
	}
}

func TestHeadlessLoginRequiresConsentAndPreservesEnrollment(t *testing.T) {
	s, provider, secrets := testLinuxStorage(t)
	p, err := s.beginLogin()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.RequiresHostConsent() {
		t.Fatal("fresh login skipped consent")
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatal("preflight created an active session", err)
	}
	m := NewManager(p)
	m.NewClient = func(string) API { t.Fatal("unaccepted consent contacted LINE"); return nil }
	if _, err := m.Login("synthetic", "synthetic", nil); !errors.Is(err, ErrHostConsent) {
		t.Fatal(err)
	}
	p.AcceptHost()
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	before, _ := s.read()
	e, _ := parseEnvelope(before)
	if len(secrets.values) != 0 || provider.seals != 1 {
		t.Fatal("native access or resealing during headless login")
	}
	p.Close()
	p, err = s.beginLogin()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.RequiresHostConsent() {
		t.Fatal("reauthentication forgot existing acceptance")
	}
	state := envelopeState()
	state.Generation = "new-login"
	state.AccessToken = "new-synthetic"
	if err := p.Save(state); err != nil {
		t.Fatal(err)
	}
	after, _ := s.read()
	next, _ := parseEnvelope(after)
	if !bytes.Equal(e.sealed, next.sealed) || provider.seals != 1 {
		t.Fatal("reauthentication changed enrolled key")
	}
}

func TestHeadlessLoginRejectsNativeAndConcurrentCreation(t *testing.T) {
	s, _, _ := testLinuxStorage(t)
	p, err := s.beginLogin()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.AcceptHost()
	if err := s.native.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	if err := p.Prepare(); !errors.Is(err, ErrStorageChanged) {
		t.Fatal(err)
	}
	if err := p.Save(envelopeState()); !errors.Is(err, ErrStorageChanged) {
		t.Fatal(err)
	}
	if _, err := s.beginLogin(); !errors.Is(err, ErrMigrationRequired) {
		t.Fatal(err)
	}
}

func TestLocalStatusVerifiesProtectionWithoutLeakingSecrets(t *testing.T) {
	s, provider, _ := testLinuxStorage(t)
	status, err := s.Status(false)
	if err != nil || status.Configured != "none" || provider.unseals != 0 {
		t.Fatal(status, err)
	}
	p, err := s.beginLogin()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.AcceptHost()
	if err := p.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	before, _ := s.read()
	status, err = s.Status(true)
	if err != nil || !status.ProtectionVerified || status.WriteAccess != "available" || status.LINEValidity != "not_checked" || status.Reboot != "expected_not_verified" {
		t.Fatal(status, err)
	}
	encoded, _ := json.Marshal(status)
	for _, secret := range []string{envelopeState().AccessToken, envelopeState().RefreshToken, envelopeState().MID, envelopeState().ExportedKeys["key"]} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("status leaked encrypted state")
		}
	}
	after, _ := s.read()
	if !bytes.Equal(before, after) || provider.seals != 1 {
		t.Fatal("status altered enrollment")
	}
	provider.fail = ErrCredentialHelper
	status, err = s.Status(false)
	if err == nil || status.Configured != "present" || status.ProtectionVerified || status.ReadAccess != "unavailable" || status.Email != "" {
		t.Fatal("locked storage mistaken for logout/verification", status, err)
	}
}

func TestNativeStatusDoesNotUnlockAndDoesNotClaimWriteReadiness(t *testing.T) {
	s, _, service := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	service.fault = func(action, account string) bool {
		if action != "search" {
			t.Fatal("status attempted native lookup/write/unlock", action)
		}
		return false
	}
	status, err := s.Status(false)
	if err != nil || status.ReadAccess != "available" || !status.ProtectionVerified {
		t.Fatal(status, err)
	}
	status, err = s.Status(true)
	if !errors.Is(err, ErrReadOnlyStatus) || status.WriteAccess == "available" {
		t.Fatal(status, err)
	}
	s.native.secrets.run = func(args []string, input []byte) secretResult {
		if args[0] != "search" || args[1] != "--all" {
			t.Fatal("unsafe native status arguments")
		}
		return secretResult{output: []byte("[/locked]\nlabel = synthetic\n"), diagnostic: true}
	}
	status, err = s.Status(false)
	if !errors.Is(err, ErrStorageUnavailable) || status.Configured != "present" || status.ProtectionVerified {
		t.Fatal(status, err)
	}
}

func TestLogoutResumesAndNeverDeletesHeadlessUnownedNativeKey(t *testing.T) {
	for _, headless := range []bool{false, true} {
		s, provider, service := testLinuxStorage(t)
		if err := s.Save(envelopeState()); err != nil {
			t.Fatal(err)
		}
		if headless {
			data, err := createHeadlessSession(s.ctx(), envelopeState(), provider, requireHost)
			if err != nil {
				t.Fatal(err)
			}
			writeCandidate(t, s, data)
		}
		originalKey := bytes.Clone(service.values["default"])
		ops := defaultFileOperations()
		syncs := 0
		ops.syncDir = func(root *os.Root) error {
			syncs++
			if syncs == 2 {
				return errors.New("directory sync failed")
			}
			return syncSessionDirectory(root)
		}
		s.native.ops = &ops
		if err := s.Delete(); !errors.Is(err, ErrDurabilityUncertain) {
			t.Fatal(err)
		}
		if !bytes.Equal(originalKey, service.values["default"]) {
			t.Fatal("key deleted before durable removal")
		}
		if _, err := s.Load(); !errors.Is(err, ErrCleanupPending) {
			t.Fatal(err)
		}
		if err := s.Save(envelopeState()); !errors.Is(err, ErrCleanupPending) {
			t.Fatal(err)
		}
		s.native.ops = nil
		provider.fail = ErrCredentialHelper
		if err := s.Delete(); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(); err != nil {
			t.Fatal(err)
		}
		_, keyRemains := service.values["default"]
		if keyRemains != headless {
			t.Fatal("incorrect native-key ownership cleanup")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(s.native.path), logoutReceiptName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cleanup receipt survived successful logout")
		}
	}
}

func TestLogoutRequiresConfirmedNativeKeyDeletion(t *testing.T) {
	s, _, service := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	originalRun := s.native.secrets.run
	s.native.secrets.run = func(args []string, input []byte) secretResult {
		if args[0] == "clear" {
			return secretResult{code: 1}
		}
		return originalRun(args, input)
	}
	if err := s.Delete(); !errors.Is(err, ErrCleanupPending) {
		t.Fatal("locked native key was reported deleted", err)
	}
	if len(service.values) != 1 {
		t.Fatal("fixture key disappeared")
	}
	s.native.secrets.run = originalRun
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutRejectsStaleReceipt(t *testing.T) {
	s, _, _ := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	ops := defaultFileOperations()
	ops.remove = func(*os.Root, string) error { return errors.New("remove failed") }
	s.native.ops = &ops
	if err := s.Delete(); err == nil {
		t.Fatal("expected interrupted logout")
	}
	s.native.ops = nil
	files, err := s.native.files(false)
	if err != nil {
		t.Fatal(err)
	}
	defer files.root.Close()
	if err := files.replace("session.enc", []byte("different session")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); !errors.Is(err, ErrStorageRepair) {
		t.Fatal("stale receipt deleted different state", err)
	}
}

func TestHeadlessRefreshAndSequenceRemainDurableBeforeActions(t *testing.T) {
	s, provider, _ := testLinuxStorage(t)
	state := envelopeState()
	state.RefreshAt = time.Unix(1, 0)
	data, err := createHeadlessSession(s.ctx(), state, provider, requireHost)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidate(t, s, data)
	api := &fakeAPI{refreshResult: &line.TokenV3IssueResult{AccessToken: "rotated", RefreshToken: "rotated-refresh"}}
	m := NewManager(s)
	m.NewClient = func(string) API { return api }
	m.Now = func() time.Time { return time.Unix(1000, 0) }
	actions := 0
	if err := m.Do(func(API) error {
		actions++
		saved, err := s.Load()
		if err != nil || saved.AccessToken != "rotated" {
			t.Fatal("request preceded refresh save", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seq, err := m.ReserveSequence()
	saved, loadErr := s.Load()
	if err != nil || loadErr != nil || seq != saved.LastReqSeq || api.refreshCalls != 1 || actions != 1 || provider.seals != 1 {
		t.Fatal("refresh/sequence contract changed", err, loadErr)
	}
	ops := defaultFileOperations()
	ops.rename = func(*os.Root, string, string) error { return errors.New("save failed") }
	s.native.ops = &ops
	m.Store = s
	if _, err := m.ReserveSequence(); err == nil {
		t.Fatal("sequence returned after failed persistence")
	}
	s.native.ops = nil
	expired, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	expired.RefreshAt = time.Unix(1, 0)
	if err := s.Save(expired); err != nil {
		t.Fatal(err)
	}
	s.native.ops = &ops
	m.Store = s
	api.refreshResult.AccessToken = "newer-remote-token"
	if err := m.Mutate(func(API) error { t.Fatal("mutation ran after failed refresh persistence"); return nil }); err == nil {
		t.Fatal("refresh save failure ignored")
	}
	if api.refreshCalls != 2 {
		t.Fatal("refresh was retried unexpectedly")
	}
}
