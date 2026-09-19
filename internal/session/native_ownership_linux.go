package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const nativePathsName = "native-paths.json"

func nativeRegistry() (*sessionFiles, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, ErrUnsafeStorage
	}
	return openSessionFiles(filepath.Join(home, ".config", "line-cli"), true)
}

func readNativePaths(files *sessionFiles) ([]string, error) {
	b, err := files.read(nativePathsName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	if len(b) > 64*1024 || json.Unmarshal(b, &paths) != nil || len(paths) > 64 {
		return nil, ErrStorageRepair
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "session.enc" || len(path) > 4096 {
			return nil, ErrStorageRepair
		}
	}
	return paths, nil
}

func canonicalSessionPath(path string) (string, error) {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

// Serialize native-item ownership across every observed config path. Keep the
// per-directory session lock as well so preceding binaries still contend there.
// Older binaries/config paths never observed here remain outside this contract.
func platformSessionLock() (func(), error) {
	registry, err := nativeRegistry()
	if err != nil {
		return nil, err
	}
	defer registry.root.Close()
	f, err := openPrivateFile(registry.root, "credential.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	globalUnlock, err := lockOpenedFile(f)
	if err != nil {
		return nil, err
	}
	unlock, err := namedLock("session.lock")
	if err != nil {
		globalUnlock()
		return nil, err
	}
	release := func() { unlock(); globalUnlock() }
	dir, err := linuxSessionDir()
	if err != nil {
		release()
		return nil, err
	}
	err = recordNativePath(registry, filepath.Join(dir, "session.enc"))
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func recordNativePath(registry *sessionFiles, sessionPath string) error {
	paths, err := readNativePaths(registry)
	if err != nil {
		return err
	}
	path, err := canonicalSessionPath(sessionPath)
	if err != nil {
		return err
	}
	for _, known := range paths {
		if known == path {
			// A prior replacement may have reached rename but failed its
			// directory sync. Reconfirm durability before relying on this
			// path record to protect shared-key ownership across a crash.
			if err := registry.ops.syncDir(registry.root); err != nil {
				return errors.Join(ErrDurabilityUncertain, err)
			}
			return nil
		}
	}
	if len(paths) >= 64 {
		return ErrStorageRepair
	}
	paths = append(paths, path)
	b, err := json.Marshal(paths)
	if len(b) > 64*1024 {
		return ErrStorageRepair
	}
	if err == nil {
		err = registry.replace(nativePathsName, b)
	}
	if err != nil {
		return err
	}
	return nil
}

func (s linuxStorage) nativeCleanupAllowed() error {
	if s.cleanupGuard != nil {
		return s.cleanupGuard()
	}
	// Tests use unique private Secret Service accounts that cannot be shared
	// by ordinary production profiles.
	if s.native.secrets.account != "" && s.native.secrets.account != "default" {
		return nil
	}
	registry, err := nativeRegistry()
	if err != nil {
		return errors.Join(ErrNativeKeyShared, err)
	}
	defer registry.root.Close()
	paths, err := readNativePaths(registry)
	if err != nil || len(paths) == 0 {
		return ErrNativeKeyShared
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ErrNativeKeyShared
	}
	current, err := canonicalSessionPath(s.native.path)
	if err != nil {
		return ErrNativeKeyShared
	}
	registered := false
	for _, path := range paths {
		if path == current {
			registered = true
			break
		}
	}
	if !registered {
		return ErrNativeKeyShared
	}
	paths = append(paths, filepath.Join(home, ".config", "line-cli", "session.enc"))
	for _, path := range paths {
		other, err := canonicalSessionPath(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ErrNativeKeyShared
		}
		if other == current {
			continue
		}
		files, err := openSessionFiles(filepath.Dir(other), false)
		if err != nil {
			return ErrNativeKeyShared
		}
		if _, err := readMigration(files); err != nil {
			files.root.Close()
			return ErrNativeKeyShared
		}
		data, readErr := files.read(filepath.Base(other))
		if readErr == nil {
			// A parse alone is insufficient evidence that another profile no
			// longer needs the native key. Authenticate and confirm durability.
			_, key, _, openErr := s.openEnvelope(data)
			clear(key)
			if openErr != nil {
				files.root.Close()
				return ErrNativeKeyShared
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			files.root.Close()
			return ErrNativeKeyShared
		}
		err = files.ops.syncDir(files.root)
		files.root.Close()
		if err != nil {
			return ErrNativeKeyShared
		}
	}
	return nil
}
