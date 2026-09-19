package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestDPAPIPreflightPreservesSessionAndCleansUp(t *testing.T) {
	s := dpapiStore{filepath.Join(t.TempDir(), "line-cli", "session.dpapi")}
	if err := prepareDPAPIStore(s); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatal("probe created an active session")
	}
	state := &State{Version: 1, MID: "test", AccessToken: "synthetic"}
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareDPAPIStore(s); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(s.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("probe replaced saved ciphertext", err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(state, got) {
		t.Fatal("probe changed saved state", err)
	}
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "session.dpapi" {
		t.Fatal("probe left temporary files", err)
	}
}
