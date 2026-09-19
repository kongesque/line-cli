package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const migrationReceiptName = "migration.pending"
const migrationCandidateName = ".migration.candidate"
const migrationMagic = "LINECLI\x00MIGRATE\x00"

// Private, bounded, nonsecret transaction metadata. The checksum detects
// corruption, not modification by code already running as this Unix user.
type migrationReceipt struct{ source, sealed, nativeKey [32]byte }

func (r migrationReceipt) encode() []byte {
	b := append([]byte(migrationMagic), r.source[:]...)
	b = append(b, r.sealed[:]...)
	b = append(b, r.nativeKey[:]...)
	sum := sha256.Sum256(b)
	return append(b, sum[:]...)
}

func readMigration(files *sessionFiles) (*migrationReceipt, error) {
	b, err := files.read(migrationReceiptName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) != 144 || !bytes.Equal(b[:16], []byte(migrationMagic)) {
		return nil, ErrStorageRepair
	}
	sum := sha256.Sum256(b[:112])
	if !bytes.Equal(sum[:], b[112:]) {
		return nil, ErrStorageRepair
	}
	r := &migrationReceipt{}
	copy(r.source[:], b[16:48])
	copy(r.sealed[:], b[48:80])
	copy(r.nativeKey[:], b[80:112])
	return r, nil
}

func (r migrationReceipt) target(data []byte) bool {
	e, err := parseEnvelope(data)
	return err == nil && sha256.Sum256(e.sealed) == r.sealed
}

func migrateHeadless(ctx context.Context, accepted bool) error {
	s, err := resolvedLinuxStorage()
	if err != nil {
		return err
	}
	s.context = ctx
	return s.migrate(accepted)
}

// Caller holds the session lock for the entire transaction. Consent is obtained
// before taking the lock; no plaintext State or provider key survives a prompt.
func (s linuxStorage) migrate(accepted bool) (result error) {
	if !accepted {
		return ErrHostConsent
	}
	files, err := s.native.files(false)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer files.root.Close()
	if pending, err := files.exists(logoutReceiptName); err != nil {
		return err
	} else if pending {
		return ErrCleanupPending
	}
	data, err := files.read(filepath.Base(s.native.path))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	r, err := readMigration(files)
	if err != nil {
		return err
	}
	defer func() {
		// Without a durable receipt this is only a disposable staged copy.
		// Once a receipt exists, preserve it and the candidate for recovery.
		if pending, err := readMigration(files); err == nil && pending == nil {
			exists, err := files.exists(migrationCandidateName)
			if err == nil && exists {
				err = files.remove(migrationCandidateName)
			}
			if err != nil {
				result = errors.Join(result, ErrStorageCleanup)
			}
		}
	}()
	if hasEnvelopeMarker(data) {
		if r != nil && !r.target(data) {
			return ErrStorageRepair
		}
		_, key, _, err := s.openEnvelope(data)
		clear(key)
		if err != nil {
			return err
		}
		if r == nil {
			return nil
		}
		// A prior rename may have returned uncertain durability. Establish it
		// before permitting native-key cleanup, even when this is only a retry.
		if err := files.ops.syncDir(files.root); err != nil {
			return errors.Join(ErrDurabilityUncertain, err)
		}
		return s.finishMigration(files, r)
	}
	if r != nil && sha256.Sum256(data) != r.source {
		return ErrStorageRepair
	}
	key, err := s.native.secrets.loadKey()
	if err != nil {
		return err
	}
	defer clear(key)
	plain, err := decryptSession(data, key)
	if err != nil {
		return err
	}
	defer clear(plain)
	state, err := decodeSession(plain)
	if err != nil {
		return err
	}
	// Refuse newer fields instead of silently dropping them during migration.
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(new(State)); err != nil {
		return ErrStorageFormat
	}
	var candidate []byte
	if r == nil {
		provider, err := s.provider(s.ctx())
		if err != nil {
			return err
		}
		candidate, err = createHeadlessSession(s.ctx(), state, provider, requireHost)
		if err != nil {
			return err
		}
		if err := files.replace(migrationCandidateName, candidate); err != nil {
			return err
		}
	} else {
		if sha256.Sum256(key) != r.nativeKey {
			return ErrStorageRepair
		}
	}
	// Reopen and authenticate the staged file with a fresh provider unseal.
	staged, err := files.read(migrationCandidateName)
	if err != nil {
		return err
	}
	if candidate != nil && !bytes.Equal(candidate, staged) {
		return ErrStorageRepair
	}
	e, wrappingKey, got, err := s.openEnvelope(staged)
	clear(wrappingKey)
	if err != nil {
		return err
	}
	if !sameStoredState(state, got) {
		return ErrStorageAuthentication
	}
	if r == nil {
		r = &migrationReceipt{source: sha256.Sum256(data), sealed: sha256.Sum256(e.sealed), nativeKey: sha256.Sum256(key)}
		if err := files.replace(migrationReceiptName, r.encode()); err != nil {
			return err
		}
	} else if !r.target(staged) {
		return ErrStorageRepair
	}
	if err := files.ops.rename(files.root, migrationCandidateName, filepath.Base(s.native.path)); err != nil {
		// Rename failed before commit. Clear the intent so ordinary native
		// operations can continue; a cleanup failure preserves the recovery gate.
		if cleanupErr := files.remove(migrationReceiptName); cleanupErr != nil {
			return errors.Join(err, cleanupErr, ErrStorageCleanup)
		}
		return err
	}
	if err := files.ops.syncDir(files.root); err != nil {
		return errors.Join(ErrDurabilityUncertain, err)
	}
	return s.finishMigration(files, r)
}

func (s linuxStorage) finishMigration(files *sessionFiles, r *migrationReceipt) error {
	if err := s.cleanupNativeKey(r.nativeKey); err != nil {
		return errors.Join(ErrMigrationPending, err)
	}
	if err := files.remove(migrationCandidateName); err != nil {
		return errors.Join(ErrMigrationPending, err)
	}
	if err := files.remove(migrationReceiptName); err != nil {
		return errors.Join(ErrMigrationPending, err)
	}
	return nil
}

func (s linuxStorage) cleanupNativeKey(expected [32]byte) error {
	remaining, err := s.native.secrets.hasKeyWithoutUnlock()
	if err != nil || !remaining {
		return err
	}
	if err := s.nativeCleanupAllowed(); err != nil {
		return err
	}
	key, err := s.native.secrets.loadKeyWithoutUnlock()
	if err != nil {
		return err
	}
	defer clear(key)
	if sha256.Sum256(key) != expected {
		return ErrNativeKeyShared
	}
	if err := s.native.secrets.Delete(); err != nil {
		return err
	}
	remaining, err = s.native.secrets.hasKeyWithoutUnlock()
	if err != nil {
		return err
	}
	if remaining {
		return ErrStorageUnavailable
	}
	return nil
}
