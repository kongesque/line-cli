package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dpapiStore struct{ path string }

func windowsStore() (dpapiStore, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return dpapiStore{}, err
	}
	return dpapiStore{filepath.Join(dir, "line-cli", "session.dpapi")}, nil
}
func (KeychainStore) Load() (*State, error) {
	s, err := windowsStore()
	if err != nil {
		return nil, err
	}
	return s.Load()
}
func (KeychainStore) Save(state *State) error {
	s, err := windowsStore()
	if err != nil {
		return err
	}
	return s.Save(state)
}
func (KeychainStore) Delete() error {
	s, err := windowsStore()
	if err != nil {
		return err
	}
	return s.Delete()
}

func protect(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > maxSessionBytes {
		return nil, errors.New("invalid Windows credential data")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	runtime.KeepAlive(data)
	if err != nil {
		return nil, errors.New("Windows could not protect/unprotect the session for this user")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	buf := unsafe.Slice(out.Data, int(out.Size))
	defer clear(buf)
	return append([]byte(nil), buf...), nil
}
func (s dpapiStore) Load() (*State, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("could not open Windows session")
	}
	defer f.Close()
	cipher, err := readSessionFile(f)
	if err != nil {
		return nil, err
	}
	plain, err := protect(cipher, false)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return decodeSession(plain)
}
func (s dpapiStore) Save(state *State) error {
	plain, err := json.Marshal(state)
	if err != nil || len(plain) > maxSessionBytes-1024 {
		return errors.New("could not encode Windows session")
	}
	defer clear(plain)
	cipher, err := protect(plain, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(cipher); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(s.path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func (s dpapiStore) Delete() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
