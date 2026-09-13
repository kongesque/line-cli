package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinuxLocksFollowSessionAcrossProcesses(t *testing.T) {
	config := t.TempDir()
	alias := filepath.Join(t.TempDir(), "config-alias")
	if err := os.Symlink(config, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session.lock", "watch.lock"} {
		t.Run(name, func(t *testing.T) {
			unlock, err := namedLock(name)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			path := filepath.Join(config, "line-cli", name)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, location := range []string{config, alias} {
				t.Setenv("XDG_CONFIG_HOME", location)
				t.Setenv("XDG_CACHE_HOME", t.TempDir())
				t.Setenv("LINE_CLI_TEST_LOCK_CHILD", name)
				cmd := exec.Command(executable, "-test.run=^TestLinuxLockChild$")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("child did not share session lock: %v\n%s", err, out)
				}
			}
			unlock()
			again, err := namedLock(name)
			if err != nil {
				t.Fatal(err)
			}
			again()
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("lock inode replaced during release")
			}
		})
	}
}

func TestLinuxLockChild(t *testing.T) {
	name := os.Getenv("LINE_CLI_TEST_LOCK_CHILD")
	if name == "" {
		return
	}
	unlock, err := namedLock(name)
	if err == nil {
		unlock()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected contention, got %v", err)
	}
}

func TestLinuxLogoutKeepsLockFiles(t *testing.T) {
	s, _ := syntheticFileStore(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(filepath.Dir(s.path)))
	unlock, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	watchUnlock, err := WatchLock()
	if err != nil {
		t.Fatal(err)
	}
	defer watchUnlock()
	if err := s.Save(&State{Version: 1, MID: "test", AccessToken: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session.lock", "watch.lock"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(s.path), name)); err != nil {
			t.Fatal("logout removed lock file", err)
		}
	}
}
