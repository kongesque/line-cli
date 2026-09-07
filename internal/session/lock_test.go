package session

import (
	"path/filepath"
	"testing"
)

func TestLockExcludesConcurrentCommandsAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	unlock, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if unexpected, err := lockFile(path); err == nil {
		unexpected()
		t.Fatal("concurrent lock succeeded")
	}
	unlock()
	unlockedAgain, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unlockedAgain()
}
