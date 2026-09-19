//go:build darwin || linux

package session

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateFileInfo(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return ErrUnsafeStorage
	}
	if directory {
		if !info.IsDir() {
			return ErrUnsafeStorage
		}
	} else if !info.Mode().IsRegular() || stat.Nlink != 1 || info.Mode().Perm()&0111 != 0 {
		return ErrUnsafeStorage
	}
	return nil
}

func openPrivateFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	f, err := root.OpenFile(name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = privateFileInfo(info, false)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func replaceSessionFile(root *os.Root, from, to string) error { return root.Rename(from, to) }

func syncSessionDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
