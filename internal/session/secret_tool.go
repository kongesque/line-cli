package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxSessionBytes = 4 << 20

func decodeSession(data []byte) (*State, error) {
	var s State
	if len(data) > maxSessionBytes || json.Unmarshal(data, &s) != nil {
		return nil, fmt.Errorf("%w: saved session is invalid; restore storage or run line logout before signing in again", ErrStorageFormat)
	}
	if s.Version != 1 || s.AccessToken == "" || s.MID == "" {
		return nil, fmt.Errorf("%w: saved session is incomplete or unsupported; restore storage or run line logout before signing in again", ErrStorageFormat)
	}
	return &s, nil
}

type secretResult struct {
	output     []byte
	code       int
	diagnostic bool
	err        error
}
type secretToolStore struct {
	run     func([]string, []byte) secretResult
	account string // empty selects the established native identity
}

func (s secretToolStore) command(action string, input []byte) secretResult {
	args := []string{action}
	account := s.account
	if account == "" {
		account = "default"
	}
	if action == "store" {
		label := "LINE CLI session"
		if account != "default" {
			label = "LINE CLI temporary storage check"
		}
		args = append(args, "--label="+label)
	}
	args = append(args, "service", "io.github.kongesque.line-cli.encryption", "account", account)
	return s.run(args, input)
}
func (s secretToolStore) loadKey() ([]byte, error) {
	r := s.command("lookup", nil)
	defer clear(r.output)
	if r.code == 1 && !r.diagnostic && len(r.output) == 0 && r.err == nil {
		return nil, ErrNotFound
	}
	if r.err != nil || r.code != 0 {
		return nil, fmt.Errorf("%w: could not read Secret Service; install secret-tool and unlock your desktop keyring", ErrStorageUnavailable)
	}
	key, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(r.output)))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, fmt.Errorf("%w: invalid session encryption key in Secret Service", ErrStorageFormat)
	}
	return key, nil
}
func (s secretToolStore) saveKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("invalid session encryption key")
	}
	data := []byte(base64.StdEncoding.EncodeToString(key))
	defer clear(data)
	r := s.command("store", data)
	defer clear(r.output)
	if r.err != nil || r.code != 0 {
		return fmt.Errorf("%w: could not save Secret Service; install secret-tool and unlock your desktop keyring", ErrStorageUnavailable)
	}
	return nil
}

func (s secretToolStore) Delete() error {
	r := s.command("clear", nil)
	defer clear(r.output)
	if r.err == nil && (r.code == 0 || (r.code == 1 && !r.diagnostic)) {
		return nil
	}
	return fmt.Errorf("%w: could not remove Secret Service session; unlock your desktop keyring", ErrStorageUnavailable)
}

// readSessionFile bounds even corrupted or replaced credential files.
func readSessionFile(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSessionBytes+1))
	if err != nil || len(b) > maxSessionBytes {
		return nil, errors.New("could not read saved session")
	}
	return b, nil
}
