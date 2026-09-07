//go:build darwin

package session

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock serializes CLI processes, including login and logout, so a concurrent
// command cannot overwrite rotated tokens or resurrect a deleted session.
func Lock() (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "line-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(dir, "session.lock"))
}

func lockFile(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session lock: %w", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("another line command is using the session; retry when it finishes")
	}
	return func() { unix.Close(fd) }, nil
}
