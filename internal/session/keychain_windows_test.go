package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDPAPISessionRoundtripReplacementAndTampering(t *testing.T) {
	s := dpapiStore{filepath.Join(t.TempDir(), "session.dpapi")}
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	state := &State{Version: 1, MID: "u-test", AccessToken: "private-token", ExportedKeys: map[string]string{"1": strings.Repeat("fake-key", 2048)}}
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	cipher, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte(state.AccessToken)) {
		t.Fatal("plaintext credential persisted")
	}
	got, err := s.Load()
	if err != nil || got.ExportedKeys["1"] != state.ExportedKeys["1"] {
		t.Fatal("DPAPI roundtrip failed", err)
	}
	state.AccessToken = "rotated"
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load()
	if err != nil || got.AccessToken != "rotated" {
		t.Fatal("replacement failed", err)
	}
	if err := os.WriteFile(s.path, []byte("invalid-ciphertext"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("invalid DPAPI blob accepted")
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
}
