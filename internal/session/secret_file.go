package session

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Secret Service stores only a random wrapping key, avoiding secret-tool's 8KiB
// input limit for sessions containing large exported Letter Sealing key sets.
type secretFileStore struct {
	path    string
	secrets secretToolStore
	ops     *fileOperations // optional per-store filesystem failure injection
}

var errMissingWrappingKey = errors.New("session encryption key is missing; restore keyring access or run line logout before signing in again")

func (s secretFileStore) files(create bool) (*sessionFiles, error) {
	files, err := openSessionFiles(filepath.Dir(s.path), create)
	if err == nil && s.ops != nil {
		files.ops = *s.ops
	}
	return files, err
}

func (s secretFileStore) Load() (*State, error) {
	files, err := s.files(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer files.root.Close()
	data, err := files.read(filepath.Base(s.path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.decode(data)
}

func (s secretFileStore) decode(data []byte) (*State, error) {
	if hasEnvelopeMarker(data) {
		return nil, ErrStorageFormat
	}
	key, err := s.secrets.loadKey()
	if errors.Is(err, ErrNotFound) {
		return nil, errMissingWrappingKey
	}
	if err != nil {
		return nil, err
	}
	defer clear(key)
	plain, err := decryptSession(data, key)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return decodeSession(plain)
}

func (s secretFileStore) Save(state *State) error {
	plain, err := json.Marshal(state)
	if err != nil || len(plain) > maxSessionBytes-1024 {
		return errors.New("could not encode credential session")
	}
	defer clear(plain)
	files, err := s.files(true)
	if err != nil {
		return err
	}
	defer files.root.Close()
	exists, err := files.exists(filepath.Base(s.path))
	if err != nil {
		return err
	}
	if exists {
		data, err := files.read(filepath.Base(s.path))
		if err != nil {
			return err
		}
		if hasEnvelopeMarker(data) {
			return ErrStorageFormat
		}
	}
	key, err := s.secrets.loadKey()
	if errors.Is(err, ErrNotFound) {
		if exists {
			return errMissingWrappingKey
		}
		key = make([]byte, 32)
		defer clear(key)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		if err = s.secrets.saveKey(key); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		defer clear(key)
	}
	data, err := encryptSession(plain, key)
	if err != nil {
		return err
	}
	return files.replace(filepath.Base(s.path), data)
}

func (s secretFileStore) Delete() error {
	files, err := s.files(false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if files != nil {
		defer files.root.Close()
		// Retain the wrapping key if removal's durability cannot be confirmed.
		if err := files.remove(filepath.Base(s.path)); err != nil {
			return err
		}
	}
	return s.secrets.Delete()
}
