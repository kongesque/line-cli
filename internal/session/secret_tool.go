package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

const maxSessionBytes = 4 << 20

func decodeSession(data []byte) (*State, error) {
	var s State
	if len(data) > maxSessionBytes || json.Unmarshal(data, &s) != nil {
		return nil, errors.New("saved session is invalid; run line login")
	}
	if s.Version != 1 || s.AccessToken == "" || s.MID == "" {
		return nil, errors.New("saved session is incomplete or unsupported; run line login")
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
	run func([]string, []byte) secretResult
}

func (s secretToolStore) command(action string, input []byte) secretResult {
	args := []string{action}
	if action == "store" {
		args = append(args, "--label=LINE CLI session")
	}
	args = append(args, "service", "io.github.kongesque.line-cli.encryption", "account", "default")
	return s.run(args, input)
}
func (s secretToolStore) loadKey() ([]byte, error) {
	r := s.command("lookup", nil)
	defer clear(r.output)
	if r.code == 1 && !r.diagnostic && len(r.output) == 0 && r.err == nil {
		return nil, ErrNotFound
	}
	if r.err != nil || r.code != 0 {
		return nil, errors.New("could not read Secret Service; install secret-tool and unlock your desktop keyring")
	}
	key, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(r.output)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid session encryption key in Secret Service")
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
	if r.err != nil || r.code != 0 {
		return errors.New("could not save Secret Service; install secret-tool and unlock your desktop keyring")
	}
	return nil
}

func (s secretToolStore) Delete() error {
	r := s.command("clear", nil)
	if r.err == nil && (r.code == 0 || (r.code == 1 && !r.diagnostic)) {
		return nil
	}
	return errors.New("could not remove Secret Service session; unlock your desktop keyring")
}

// readSessionFile bounds even corrupted or replaced credential files.
func readSessionFile(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSessionBytes+1))
	if err != nil || len(b) > maxSessionBytes {
		return nil, errors.New("could not read saved session")
	}
	return b, nil
}
