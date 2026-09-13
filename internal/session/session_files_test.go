package session

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func testSessionFiles(t *testing.T) *sessionFiles {
	t.Helper()
	f, err := openSessionFiles(filepath.Join(t.TempDir(), "line-cli"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.root.Close() })
	return f
}

func TestSessionReplacementFailuresKeepOldFile(t *testing.T) {
	failure := errors.New("injected filesystem failure")
	for _, stage := range []string{"write", "short_write", "sync", "close", "rename"} {
		t.Run(stage, func(t *testing.T) {
			files := testSessionFiles(t)
			if err := files.replace("session.enc", []byte("original")); err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "write":
				files.ops.write = func(*os.File, []byte) (int, error) { return 0, failure }
			case "short_write":
				files.ops.write = func(*os.File, []byte) (int, error) { return 0, nil }
			case "sync":
				files.ops.sync = func(*os.File) error { return failure }
			case "close":
				files.ops.close = func(f *os.File) error { _ = f.Close(); return failure }
			case "rename":
				files.ops.rename = func(*os.Root, string, string) error { return failure }
			}
			err := files.replace("session.enc", []byte("replacement"))
			if err == nil || errors.Is(err, ErrDurabilityUncertain) {
				t.Fatal("incorrect pre-commit outcome", err)
			}
			if stage == "short_write" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatal(err)
			}
			got, err := files.read("session.enc")
			if err != nil || string(got) != "original" {
				t.Fatal("original file changed", err)
			}
			entries, err := os.ReadDir(files.root.Name())
			if err != nil || len(entries) != 1 || entries[0].Name() != "session.enc" {
				t.Fatal("temporary file remains", err)
			}
		})
	}
}

func TestSessionDirectorySyncFailureReportsCommittedState(t *testing.T) {
	f := testSessionFiles(t)
	if err := f.replace("session.enc", []byte("old")); err != nil {
		t.Fatal(err)
	}
	f.ops.syncDir = func(*os.Root) error { return errors.New("sync failed") }
	if err := f.replace("session.enc", []byte("new")); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal(err)
	}
	got, err := f.read("session.enc")
	if err != nil || string(got) != "new" {
		t.Fatal("replacement rolled back", err)
	}
	if err := f.remove("session.enc"); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal(err)
	}
	if _, err := f.root.Lstat("session.enc"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file survived removal", err)
	}
	if err := f.remove("session.enc"); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal("retry skipped directory sync", err)
	}
	f.ops = defaultFileOperations()
	if err := f.remove("session.enc"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionFailedTemporaryCleanupIsReported(t *testing.T) {
	f := testSessionFiles(t)
	f.ops.write = func(*os.File, []byte) (int, error) { return 0, io.ErrShortWrite }
	f.ops.remove = func(*os.Root, string) error { return errors.New("remove failed") }
	if err := f.replace("session.enc", []byte("data")); !errors.Is(err, ErrStorageCleanup) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("cleanup failure lost", err)
	}
}

func TestSessionFilesRejectTraversalAndOversize(t *testing.T) {
	f := testSessionFiles(t)
	for _, name := range []string{"../session.enc", "child/session.enc", ".", ".."} {
		if err := f.replace(name, nil); !errors.Is(err, ErrUnsafeStorage) {
			t.Fatal("unsafe name accepted", name, err)
		}
	}
	data := bytes.Repeat([]byte("x"), maxSessionBytes+1)
	if err := f.replace("session.enc", data); err == nil {
		t.Fatal("oversized write accepted")
	}
	if err := f.root.WriteFile("session.enc", data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.read("session.enc"); err == nil {
		t.Fatal("oversized read accepted")
	}
}

func TestSessionCipherPreservesLegacyFormat(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	plain, _ := json.Marshal(&State{Version: 1, MID: "synthetic", AccessToken: "synthetic-token"})
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := bytes.Repeat([]byte{2}, aead.NonceSize())
	legacy := aead.Seal(nonce, nonce, plain, []byte("line-cli-session-v1"))
	got, err := decryptSession(legacy, key)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("legacy ciphertext rejected", err)
	}
	current, err := encryptSession(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err = aead.Open(nil, current[:aead.NonceSize()], current[aead.NonceSize():], []byte("line-cli-session-v1"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("native format changed", err)
	}
	for _, n := range []int{0, 16, 24, 31, 33} {
		if _, err := encryptSession(plain, make([]byte, n)); err == nil {
			t.Fatal("wrong key size accepted")
		}
	}
	current[len(current)-1] ^= 1
	if _, err := decryptSession(current, key); err == nil {
		t.Fatal("tampered session accepted")
	}
}
