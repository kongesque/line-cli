package session

import (
	"os"
	"path/filepath"
)

func platformSessionLock() (func(), error) { return namedLock("session.lock") }

func sessionLockDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "line-cli"), nil
}
