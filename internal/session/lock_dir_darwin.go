package session

import (
	"os"
	"path/filepath"
)

func sessionLockDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "line-cli"), nil
}
