package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exported only in the test binary so the external watcher regression can use
// the real resolver/transaction with synthetic providers, without an import cycle.
func NewMigrationFixtureForTest(t *testing.T, state *State) (Store, func() error) {
	t.Helper()
	s, _, _ := testLinuxStorage(t)
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	return s, func() error { return s.migrate(true) }
}

func TestMigrationPreservesStateAndResolvesCurrentBackend(t *testing.T) {
	s, provider, secrets := testLinuxStorage(t)
	state := envelopeState()
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	before, _ := s.read()
	m := NewManager(s)
	m.Now = func() time.Time { return time.UnixMilli(1) }
	if _, err := m.Store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(false); !errors.Is(err, ErrHostConsent) {
		t.Fatal(err)
	}
	after, _ := s.read()
	if !bytes.Equal(before, after) || provider.seals != 0 {
		t.Fatal("missing consent changed storage")
	}
	if err := s.migrate(true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !sameStoredState(state, got) {
		t.Fatal("migration lost state", err)
	}
	if len(secrets.values) != 0 || provider.seals != 1 {
		t.Fatal("native key retained or multiple keys enrolled")
	}
	// A long-lived manager resolves storage again after migration, preserving
	// the current request sequence, checkpoint, generation, and rotated token.
	seq, err := m.ReserveSequence()
	if err != nil || seq != state.LastReqSeq+1 {
		t.Fatal("sequence did not continue", err)
	}
	got, err = s.Load()
	if err != nil || *got.WatchRevision != *state.WatchRevision || got.Generation != state.Generation {
		t.Fatal("checkpoint/generation changed", err)
	}
	got.AccessToken = "rotated-after-migration"
	if err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(true); err != nil || provider.seals != 1 {
		t.Fatal("repeat migration resealed", err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatal("logout resurrected state", err)
	}
}

func TestMigrationResumeRefusesChangedSourceAndCandidate(t *testing.T) {
	for _, changed := range []string{"source", "candidate"} {
		t.Run(changed, func(t *testing.T) {
			s, _, service := testLinuxStorage(t)
			if err := s.Save(envelopeState()); err != nil {
				t.Fatal(err)
			}
			ops := defaultFileOperations()
			syncs := 0
			ops.syncDir = func(root *os.Root) error {
				syncs++
				if syncs == 2 {
					return errors.New("receipt durability uncertain")
				}
				return syncSessionDirectory(root)
			}
			s.native.ops = &ops
			if err := s.migrate(true); !errors.Is(err, ErrDurabilityUncertain) {
				t.Fatal(err)
			}
			s.native.ops = nil
			if _, err := s.Load(); !errors.Is(err, ErrMigrationPending) {
				t.Fatal("uncommitted intent did not gate writes", err)
			}
			if changed == "source" {
				state := envelopeState()
				state.AccessToken = "newer-synthetic-token"
				if err := s.native.Save(state); err != nil {
					t.Fatal(err)
				}
			} else {
				files, err := s.native.files(false)
				if err != nil {
					t.Fatal(err)
				}
				defer files.root.Close()
				data, err := files.read(migrationCandidateName)
				if err != nil {
					t.Fatal(err)
				}
				data[len(data)-1] ^= 1
				if err := files.replace(migrationCandidateName, data); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.read()
			if err := s.migrate(true); err == nil {
				t.Fatal("changed transaction accepted")
			}
			after, _ := s.read()
			if !bytes.Equal(before, after) || len(service.values) != 1 {
				t.Fatal("recovery overwrote newer state or deleted key")
			}
		})
	}
}

func TestMigrationFailuresRecoverWithoutLosingSourceKey(t *testing.T) {
	for _, mode := range []string{"stage_write", "stage_sync", "stage_rename", "receipt_write", "receipt_sync", "commit_rename", "commit_sync", "key_clear", "receipt_remove"} {
		t.Run(mode, func(t *testing.T) {
			s, provider, service := testLinuxStorage(t)
			if err := s.Save(envelopeState()); err != nil {
				t.Fatal(err)
			}
			original, _ := s.read()
			key := bytes.Clone(service.values["default"])
			ops := defaultFileOperations()
			fail := errors.New("synthetic failure")
			writes, syncs := 0, 0
			ops.write = func(f *os.File, b []byte) (int, error) {
				writes++
				if (mode == "stage_write" && writes == 1) || (mode == "receipt_write" && writes == 2) {
					return 0, fail
				}
				return f.Write(b)
			}
			ops.sync = func(f *os.File) error {
				if mode == "stage_sync" {
					return fail
				}
				return f.Sync()
			}
			ops.rename = func(root *os.Root, from, to string) error {
				if (mode == "stage_rename" && to == migrationCandidateName) || (mode == "commit_rename" && to == "session.enc") {
					return fail
				}
				return root.Rename(from, to)
			}
			ops.syncDir = func(root *os.Root) error {
				syncs++
				if (mode == "receipt_sync" && syncs == 2) || (mode == "commit_sync" && syncs == 3) {
					return fail
				}
				return syncSessionDirectory(root)
			}
			ops.remove = func(root *os.Root, name string) error {
				if mode == "receipt_remove" && name == migrationReceiptName {
					return fail
				}
				return root.Remove(name)
			}
			s.native.ops = &ops
			if mode == "key_clear" {
				service.fault = func(action, account string) bool { return action == "clear" }
			}
			err := s.migrate(true)
			if err == nil {
				t.Fatal("fault ignored")
			}
			after, _ := s.read()
			committed := mode == "commit_sync" || mode == "key_clear" || mode == "receipt_remove"
			if hasEnvelopeMarker(after) != committed {
				t.Fatal("unexpected commit boundary")
			}
			if !committed && !bytes.Equal(original, after) {
				t.Fatal("source changed before commit")
			}
			if mode != "receipt_remove" && !bytes.Equal(key, service.values["default"]) {
				t.Fatal("source key deleted too early")
			}
			if mode == "commit_sync" && !errors.Is(err, ErrDurabilityUncertain) {
				t.Fatal("uncertain commit not reported", err)
			}
			s.native.ops = nil
			service.fault = nil
			seals := provider.seals
			if err := s.migrate(true); err != nil {
				t.Fatal("retry failed", err)
			}
			if committed && provider.seals != seals {
				t.Fatal("cleanup retry enrolled another key")
			}
			got, err := s.Load()
			if err != nil || !sameStoredState(got, envelopeState()) {
				t.Fatal("retry lost state", err)
			}
			if len(service.values) != 0 {
				t.Fatal("retry left native key")
			}
		})
	}
}

func TestMigrationCleanupPendingAllowsUpdatesAndProtectsReplacementKey(t *testing.T) {
	s, provider, service := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	s.cleanupGuard = func() error { return ErrNativeKeyShared }
	if err := s.migrate(true); !errors.Is(err, ErrMigrationPending) {
		t.Fatal(err)
	}
	status, err := s.Status(false)
	if !errors.Is(err, ErrMigrationPending) || status.Backend != "headless" || status.ReadAccess != "available" || !status.ProtectionVerified {
		t.Fatal("pending cleanup misreported", status, err)
	}
	state, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.LastReqSeq++
	*state.WatchRevision++
	state.AccessToken = "synthetic-rotated"
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	old := bytes.Clone(service.values["default"])
	if err := s.native.secrets.saveKey(bytes.Repeat([]byte{99}, 32)); err != nil {
		t.Fatal(err)
	}
	s.cleanupGuard = func() error { return nil }
	if err := s.migrate(true); !errors.Is(err, ErrNativeKeyShared) {
		t.Fatal("replacement key deleted", err)
	}
	service.values["default"] = old
	if err := s.migrate(true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !sameStoredState(state, got) || provider.seals != 1 {
		t.Fatal("cleanup rolled back updates", err)
	}
}

func TestMigrationLogoutResumesWithoutBrokerOrRepeatedNativeDeletion(t *testing.T) {
	s, provider, service := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	service.fault = func(action, account string) bool { return action == "clear" }
	if err := s.migrate(true); !errors.Is(err, ErrMigrationPending) {
		t.Fatal(err)
	}
	provider.fail = ErrCredentialHelper
	if err := s.Delete(); !errors.Is(err, ErrCleanupPending) {
		t.Fatal(err)
	}
	service.fault = nil
	ops := defaultFileOperations()
	ops.remove = func(root *os.Root, name string) error {
		if name == logoutReceiptName {
			return errors.New("receipt removal failed")
		}
		return root.Remove(name)
	}
	s.native.ops = &ops
	if err := s.Delete(); err == nil {
		t.Fatal("logout fault ignored")
	}
	// Cleanup completed but final logout receipt remained. A later key must
	// never be cleared again when retrying that receipt.
	if err := s.native.secrets.saveKey(bytes.Repeat([]byte{55}, 32)); err != nil {
		t.Fatal(err)
	}
	s.native.ops = nil
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if len(service.values) != 1 {
		t.Fatal("retry deleted unrelated new native key")
	}
}

func TestMigrationRejectsUnknownFieldsAndDamagedReceipts(t *testing.T) {
	s, _, service := testLinuxStorage(t)
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	key, err := s.native.secrets.loadKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	data, _ := s.read()
	plain, err := decryptSession(data, key)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	plain = append(plain[:len(plain)-1], []byte(",\"future_field\":\"preserve-me\"}")...)
	data, err = encryptSession(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidate(t, s, data)
	if err := s.migrate(true); !errors.Is(err, ErrStorageFormat) {
		t.Fatal("unknown state lost", err)
	}
	after, _ := s.read()
	if !bytes.Equal(data, after) || len(service.values) != 1 {
		t.Fatal("invalid migration changed state")
	}
	files, err := s.native.files(false)
	if err != nil {
		t.Fatal(err)
	}
	defer files.root.Close()
	r := migrationReceipt{source: sha256.Sum256(data)}.encode()
	r[50] ^= 1
	if err := files.replace(migrationReceiptName, r); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(true); !errors.Is(err, ErrStorageRepair) {
		t.Fatal(err)
	}
	if err := s.Delete(); !errors.Is(err, ErrStorageRepair) {
		t.Fatal("corrupt ownership receipt authorized deletion", err)
	}
}

func TestKnownNativeProfilesPreventKeyDeletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, _, service := testLinuxStorage(t)
	s.cleanupGuard = nil
	registry, err := nativeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.root.Close()
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	if err := recordNativePath(registry, s.native.path); err != nil {
		t.Fatal(err)
	}
	other := s
	other.native.path = filepath.Join(t.TempDir(), "line-cli", "session.enc")
	if err := other.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	if err := recordNativePath(registry, other.native.path); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(true); !errors.Is(err, ErrNativeKeyShared) {
		t.Fatal("shared native key was deleted", err)
	}
	if _, err := other.Load(); err != nil {
		t.Fatal("other native profile lost its key", err)
	}
	if err := other.migrate(true); err != nil {
		t.Fatal("last native profile could not finish", err)
	}
	if err := s.migrate(true); err != nil {
		t.Fatal(err)
	}
	if len(service.values) != 0 {
		t.Fatal("native key survived all migrations")
	}
}

func TestLinuxSystemdMigrationIntegration(t *testing.T) {
	if os.Getenv("LINE_CLI_TEST_SYSTEMD_CREDS") != "1" {
		t.Skip("requires disposable booted Linux VM")
	}
	s, _, service := testLinuxStorage(t)
	s.provider = newSystemdCredentials
	s.context = context.Background()
	if err := s.Save(envelopeState()); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !sameStoredState(envelopeState(), got) {
		t.Fatal("native systemd migration lost state", err)
	}
	if len(service.values) != 0 {
		t.Fatal("native key cleanup incomplete")
	}
}
