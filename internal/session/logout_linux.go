package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
)

const logoutReceiptName = "logout.pending"
const logoutReceiptMagic = "LINECLI\x00LOGOUT\x00"

func (s linuxStorage) checkCleanup() error {
	files, err := s.native.files(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer files.root.Close()
	exists, err := files.exists(logoutReceiptName)
	if err != nil {
		return err
	}
	if exists {
		return ErrCleanupPending
	}
	r, err := readMigration(files)
	if err != nil {
		return err
	}
	if r != nil {
		data, err := files.read(filepath.Base(s.native.path))
		if err != nil {
			return ErrStorageRepair
		}
		if !r.target(data) {
			if sha256.Sum256(data) == r.source {
				return ErrMigrationPending
			}
			return ErrStorageRepair
		}
	}
	return nil
}

func (s linuxStorage) Delete() error {
	files, err := s.native.files(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer files.root.Close()
	name := filepath.Base(s.native.path)
	data, err := files.read(name)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	migration, err := readMigration(files)
	if err != nil {
		return err
	}
	if migration != nil && !missing && !migration.target(data) && sha256.Sum256(data) != migration.source {
		return ErrStorageRepair
	}
	receipt, err := files.read(logoutReceiptName)
	if errors.Is(err, os.ErrNotExist) {
		// Absence cannot authorize deleting a native key that may belong to a
		// different known profile or a formerly native session.
		if missing {
			if migration != nil {
				return ErrStorageRepair
			}
			return nil
		}
		backend := byte(1)
		if hasEnvelopeMarker(data) {
			if len(data) < 20 || data[16] != envelopeVersion || data[17] != 0 || data[18] != backendSystemd || data[19] != 0 {
				return ErrStorageRepair
			}
			backend = 2
		}
		if migration != nil {
			backend = 3
		}
		sum := sha256.Sum256(data)
		receipt = append([]byte(logoutReceiptMagic), backend)
		receipt = append(receipt, sum[:]...)
		checksum := sha256.Sum256(receipt)
		receipt = append(receipt, checksum[:]...)
		if err := files.replace(logoutReceiptName, receipt); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if len(receipt) != 80 || !bytes.Equal(receipt[:15], []byte(logoutReceiptMagic)) || (receipt[15] != 1 && receipt[15] != 2 && receipt[15] != 3) {
		return ErrStorageRepair
	}
	checksum := sha256.Sum256(receipt[:48])
	if !bytes.Equal(checksum[:], receipt[48:]) {
		return ErrStorageRepair
	}
	if !missing {
		sum := sha256.Sum256(data)
		if !bytes.Equal(sum[:], receipt[16:48]) {
			return ErrStorageRepair
		}
	}
	if err := files.remove(name); err != nil {
		return err
	}
	if migration != nil {
		if err := s.finishMigration(files, migration); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
	} else if receipt[15] == 1 {
		if err := s.nativeCleanupAllowed(); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		if err := s.native.secrets.Delete(); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		remaining, err := s.native.secrets.hasKeyWithoutUnlock()
		if err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		if remaining {
			return ErrCleanupPending
		}
	}
	if err := files.remove(migrationCandidateName); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	return files.remove(logoutReceiptName)
}
