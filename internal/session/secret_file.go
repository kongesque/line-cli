package session

import (
	"crypto/aes"
	"crypto/cipher"
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
}

func (s secretFileStore) Load() (*State, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readSessionFile(f)
	if err != nil {
		return nil, err
	}
	key, err := s.secrets.loadKey()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("invalid encrypted session")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("line-cli-session-v1"))
	if err != nil {
		return nil, errors.New("could not authenticate encrypted session; run line login")
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
	key, err := s.secrets.loadKey()
	if errors.Is(err, ErrNotFound) {
		if _, statErr := os.Stat(s.path); !errors.Is(statErr, os.ErrNotExist) {
			return errors.New("session encryption key is missing; run line logout before signing in again")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		if err = s.secrets.saveKey(key); err != nil {
			clear(key)
			return err
		}
	} else if err != nil {
		return err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	data := aead.Seal(nonce, nonce, plain, []byte("line-cli-session-v1"))
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
func (s secretFileStore) Delete() error {
	// Remove ciphertext first; an interrupted logout must not leave readable data.
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.secrets.Delete()
}
