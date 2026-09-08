package session

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// Run only in the disposable D-Bus session created by CI. Never use a personal
// keyring: the production service/account are deliberately exercised here.
func TestLinuxNativeSecretService(t *testing.T) {
	if os.Getenv("LINE_CLI_TEST_SECRET_SERVICE") != "1" {
		t.Skip("requires an explicitly enabled, isolated Secret Service session")
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Fatal("isolated D-Bus session is required")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, err := linuxStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.secrets.loadKey(); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected an empty test keyring; refusing to modify an existing item")
	}
	t.Cleanup(func() {
		if err := store.Delete(); err != nil {
			t.Error("clean up test credentials:", err)
		}
	})
	api := KeychainStore{}
	state := &State{Version: 1, MID: "u-test", AccessToken: "synthetic-token", ExportedKeys: map[string]string{"1": strings.Repeat("synthetic-key", 4096)}}
	if err := api.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err := (KeychainStore{}).Load()
	if err != nil || got.AccessToken != state.AccessToken || got.ExportedKeys["1"] != state.ExportedKeys["1"] {
		t.Fatal("native keyring roundtrip failed", err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil || bytes.Contains(data, []byte(state.AccessToken)) || bytes.Contains(data, []byte("synthetic-key")) {
		t.Fatal("session ciphertext missing or contains plaintext", err)
	}
	info, err := os.Stat(store.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("session file permissions must be 0600", err)
	}
	state.AccessToken = "rotated-synthetic-token"
	if err := api.Save(state); err != nil {
		t.Fatal(err)
	}
	got, err = api.Load()
	if err != nil || got.AccessToken != state.AccessToken {
		t.Fatal("native keyring replacement failed", err)
	}
	data[0] ^= 1
	if err := os.WriteFile(store.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Load(); err == nil {
		t.Fatal("tampered session accepted")
	}
	if err := api.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Load(); !errors.Is(err, ErrNotFound) {
		t.Fatal("session survived deletion")
	}
	if _, err := store.secrets.loadKey(); !errors.Is(err, ErrNotFound) {
		t.Fatal("keyring item survived deletion")
	}
	if err := api.Delete(); err != nil {
		t.Fatal("repeated deletion failed", err)
	}
}
