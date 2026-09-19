//go:build darwin || linux

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// Lock serializes CLI processes, including login and logout, so a concurrent
// command cannot overwrite rotated tokens or resurrect a deleted session.
var ErrBusy = errors.New("another line command is using the session; retry when it finishes")

func Lock() (func(), error) { return platformSessionLock() }

// WatchLock prevents two consumers from advancing the same event cursor.
func WatchLock() (func(), error) {
	unlock, err := namedLock("watch.lock")
	if errors.Is(err, ErrBusy) {
		return nil, errors.New("another line watch is already running")
	}
	return unlock, err
}

func namedLock(name string) (func(), error) {
	dir, err := sessionLockDir()
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" {
		files, err := openSessionFiles(dir, true)
		if err != nil {
			return nil, err
		}
		defer files.root.Close()
		f, err := openPrivateFile(files.root, name, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		return lockOpenedFile(f)
	}
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
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil {
		err = privateFileInfo(info, false)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return lockOpenedFile(f)
}

func lockOpenedFile(f *os.File) (func(), error) {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire session lock: %w", err)
	}
	return func() { f.Close() }, nil
}
