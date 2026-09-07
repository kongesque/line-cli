//go:build darwin || linux

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock serializes CLI processes, including login and logout, so a concurrent
// command cannot overwrite rotated tokens or resurrect a deleted session.
var ErrBusy = errors.New("another line command is using the session; retry when it finishes")

func Lock() (func(), error) { return namedLock("session.lock") }

// WatchLock prevents two consumers from advancing the same event cursor.
func WatchLock() (func(), error) {
	unlock, err := namedLock("watch.lock")
	if errors.Is(err, ErrBusy) {
		return nil, errors.New("another line watch is already running")
	}
	return unlock, err
}

func namedLock(name string) (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "line-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(dir, name))
}

func lockFile(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session lock: %w", err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire session lock: %w", err)
	}
	return func() { unix.Close(fd) }, nil
}
