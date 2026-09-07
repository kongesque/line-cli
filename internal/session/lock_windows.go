package session

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

var ErrBusy = errors.New("another line command is using the session; retry when it finishes")

func Lock() (func(), error) { return namedLock("session.lock") }
func WatchLock() (func(), error) {
	u, err := namedLock("watch.lock")
	if errors.Is(err, ErrBusy) {
		return nil, errors.New("another line watch is already running")
	}
	return u, err
}
func namedLock(name string) (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "line-cli")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(dir, name))
}
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
