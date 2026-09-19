package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativePathRegistrationRetryConfirmsDurability(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry, err := nativeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer registry.root.Close()
	path := filepath.Join(t.TempDir(), "session.enc")
	syncs := 0
	registry.ops.syncDir = func(*os.Root) error { syncs++; return errors.New("synthetic directory sync failure") }
	if err := recordNativePath(registry, path); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal(err)
	}
	// The new record is visible, but a retry still cannot use it while its
	// directory is not known durable. This must happen before session writes.
	if err := recordNativePath(registry, path); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal("retry trusted an unsynced path record", err)
	}
	if syncs != 2 {
		t.Fatal("retry skipped durability confirmation")
	}
	registry.ops.syncDir = syncSessionDirectory
	if err := recordNativePath(registry, path); err != nil {
		t.Fatal(err)
	}
	paths, err := readNativePaths(registry)
	if err != nil || len(paths) != 1 {
		t.Fatal("retry duplicated or lost registration", err)
	}
}
