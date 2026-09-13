//go:build darwin || linux

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSessionFilesRejectUnsafeTypesAndPermissions(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			f := testSessionFiles(t)
			path := filepath.Join(f.root.Name(), "session.enc")
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(target, path)
			case "hardlink":
				err = os.Link(target, path)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "permissions":
				err = os.WriteFile(path, []byte("unsafe"), 0600)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.read("session.enc"); !errors.Is(err, ErrUnsafeStorage) {
				t.Fatal("unsafe read", err)
			}
			if err := f.replace("session.enc", nil); !errors.Is(err, ErrUnsafeStorage) {
				t.Fatal("unsafe replacement", err)
			}
			if err := f.remove("session.enc"); !errors.Is(err, ErrUnsafeStorage) {
				t.Fatal("unsafe removal", err)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "untouched" {
				t.Fatal("external target changed", err)
			}
		})
	}
}

func TestSessionDirectoryIsPrivateWithoutChmoddingParents(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "line-cli")
	f, err := openSessionFiles(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	f.root.Close()
	info, _ := os.Stat(parent)
	if info.Mode().Perm() != 0755 {
		t.Fatal("changed caller-owned parent permissions")
	}
	info, _ = os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("new directory is not private")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if f, err := openSessionFiles(dir, true); !errors.Is(err, ErrUnsafeStorage) {
		if f != nil {
			f.root.Close()
		}
		t.Fatal("insecure directory accepted", err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if f, err := openSessionFiles(alias, true); !errors.Is(err, ErrUnsafeStorage) {
		if f != nil {
			f.root.Close()
		}
		t.Fatal("application directory symlink accepted", err)
	}
}
