package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretToolUsesStdinAndSanitizesErrors(t *testing.T) {
	state := &State{Version: 1, MID: "u-test", AccessToken: "private-token"}
	var saved []byte
	helper := secretToolStore{run: func(args []string, input []byte) secretResult {
		if strings.Contains(strings.Join(args, " "), state.AccessToken) {
			t.Fatal("token in process arguments")
		}
		switch args[0] {
		case "store":
			saved = append([]byte(nil), input...)
		case "lookup":
			if len(saved) == 0 {
				return secretResult{code: 1}
			}
			return secretResult{output: append([]byte(nil), saved...)}
		}
		return secretResult{}
	}}
	store := secretFileStore{path: filepath.Join(t.TempDir(), "session.enc"), secrets: helper}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got.AccessToken != state.AccessToken {
		t.Fatal("roundtrip failed", err)
	}
	cipher, err := os.ReadFile(store.path)
	if err != nil || bytes.Contains(cipher, []byte(state.AccessToken)) {
		t.Fatal("plaintext session file", err)
	}
	state.ExportedKeys = map[string]string{"1": strings.Repeat("large-key", 4096)}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load()
	if err != nil || got.ExportedKeys["1"] != state.ExportedKeys["1"] {
		t.Fatal("large key set truncated", err)
	}
	store.secrets.run = func([]string, []byte) secretResult {
		return secretResult{code: 1, diagnostic: true, err: errors.New("private-token")}
	}
	if _, err = store.Load(); err == nil || strings.Contains(err.Error(), "private-token") || errors.Is(err, ErrNotFound) {
		t.Fatal("unsafe helper error", err)
	}
}
func TestSecretToolMissingAndInvalidSessions(t *testing.T) {
	s := secretToolStore{run: func([]string, []byte) secretResult { return secretResult{code: 1} }}
	if _, err := s.loadKey(); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{}`, `{"version":2,"mid":"u","access_token":"secret"}`, `secret-not-json`} {
		if _, err := decodeSession([]byte(data)); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid session accepted/leaked")
		}
	}
	data, _ := json.Marshal(&State{Version: 1, MID: "u", AccessToken: "token"})
	if _, err := decodeSession(data); err != nil {
		t.Fatal(err)
	}
}
