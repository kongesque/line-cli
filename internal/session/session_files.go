package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var (
	ErrUnsafeStorage = errors.New("session storage has unsafe ownership, permissions, or file type")
	// Replacement/removal has happened, but its survival across a crash is unknown.
	ErrDurabilityUncertain = errors.New("session storage changed but its durability could not be confirmed")
	ErrStorageCleanup      = errors.New("temporary storage cleanup failed")
)

// Per-operation hooks permit failure tests without changing process-global state.
type fileOperations struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	rename  func(*os.Root, string, string) error
	remove  func(*os.Root, string) error
	syncDir func(*os.Root) error
}

func defaultFileOperations() fileOperations {
	return fileOperations{
		write: (*os.File).Write, sync: (*os.File).Sync, close: (*os.File).Close,
		rename: replaceSessionFile, remove: (*os.Root).Remove, syncDir: syncSessionDirectory,
	}
}

type sessionFiles struct {
	root *os.Root
	ops  fileOperations
}

func openSessionFiles(path string, create bool) (*sessionFiles, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := durableMkdirAll(path); err != nil {
			return nil, err
		}
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := privateFileInfo(before, true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, ErrUnsafeStorage
	}
	return &sessionFiles{root: root, ops: defaultFileOperations()}, nil
}

// Only new directories are created with private modes. Existing user-selected
// parents are never chmodded. Persist each new entry in its parent directory.
func durableMkdirAll(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return ErrUnsafeStorage
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return ErrUnsafeStorage
	}
	if err := durableMkdirAll(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	return syncSessionDirectory(root)
}

func (s *sessionFiles) exists(name string) (bool, error) {
	// Session operations use single components, never caller-supplied subpaths.
	if filepath.Base(name) != name || name == "." || name == ".." {
		return false, ErrUnsafeStorage
	}
	info, err := s.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, privateFileInfo(info, false)
}

func (s *sessionFiles) read(name string) ([]byte, error) {
	if exists, err := s.exists(name); err != nil {
		return nil, err
	} else if !exists {
		return nil, os.ErrNotExist
	}
	f, err := openPrivateFile(s.root, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readSessionFile(f)
}

func (s *sessionFiles) replace(name string, data []byte) (result error) {
	if len(data) > maxSessionBytes {
		return errors.New("session ciphertext exceeds the storage limit")
	}
	if _, err := s.exists(name); err != nil {
		return err
	}
	temporary := ".session-" + rand.Text()
	f, err := openPrivateFile(s.root, temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		// Close before removal, including failed writes (required on Windows).
		_ = f.Close()
		if renamed {
			return
		}
		if err := s.ops.remove(s.root, temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, ErrStorageCleanup)
		} else if err := s.ops.syncDir(s.root); err != nil {
			result = errors.Join(result, ErrStorageCleanup)
		}
	}()
	n, err := s.ops.write(f, data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := s.ops.sync(f); err != nil {
		return err
	}
	if err := s.ops.close(f); err != nil {
		return err
	}
	if _, err := s.exists(name); err != nil {
		return err
	}
	if err := s.ops.rename(s.root, temporary, name); err != nil {
		return err
	}
	renamed = true
	if err := s.ops.syncDir(s.root); err != nil {
		return fmt.Errorf("%w: %w", ErrDurabilityUncertain, err)
	}
	return nil
}

func (s *sessionFiles) remove(name string) error {
	if exists, err := s.exists(name); err != nil {
		return err
	} else if exists {
		if err := s.ops.remove(s.root, name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Also sync an already absent entry: a previous attempt may have removed it
	// without confirming durability, and cleanup of its wrapping key must wait.
	if err := s.ops.syncDir(s.root); err != nil {
		return fmt.Errorf("%w: %w", ErrDurabilityUncertain, err)
	}
	return nil
}
