//go:build !darwin && !linux

package session

import "os"

// secretFileStore is the Linux provider. These fallbacks keep synthetic tests
// portable; Windows production storage continues to use DPAPI and MoveFileEx.
func privateFileInfo(info os.FileInfo, directory bool) error {
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return ErrUnsafeStorage
	}
	return nil
}

func openPrivateFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return nil, ErrUnsafeStorage
	}
	return root.OpenFile(name, flags, mode)
}

func replaceSessionFile(root *os.Root, from, to string) error { return root.Rename(from, to) }
func syncSessionDirectory(*os.Root) error                     { return nil }
