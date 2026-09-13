package session

import (
	"errors"
	"os"
	"path/filepath"
)

func prepareNativeStorage() error {
	store, err := windowsStore()
	if err != nil {
		return err
	}
	return prepareDPAPIStore(store)
}

func prepareDPAPIStore(store dpapiStore) (result error) {
	if err := checkSavedStorage(store); err != nil {
		return err
	}
	parent := filepath.Dir(store.path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(parent, ".storage-probe-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(dir); err != nil {
			result = errors.Join(result, ErrStorageCleanup)
		}
	}()
	probe := dpapiStore{filepath.Join(dir, "session.dpapi")}
	return exerciseStorage(probe, func() error { return removeStorageProbe(probe) })
}
