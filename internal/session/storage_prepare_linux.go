package session

import (
	"crypto/rand"
	"errors"
	"path/filepath"
)

func prepareNativeStorage() error {
	store, err := linuxStore()
	if err != nil {
		return err
	}
	return prepareSecretFileStore(store)
}

func prepareSecretFileStore(store secretFileStore) (result error) {
	files, err := store.files(true)
	if err != nil {
		return err
	}
	defer files.root.Close()
	exists, err := files.exists(filepath.Base(store.path))
	if err != nil {
		return err
	}
	if exists {
		if err := checkSavedStorage(store); err != nil {
			return err
		}
	} else {
		key, err := store.secrets.loadKey()
		clear(key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	name := ".storage-probe-" + rand.Text()
	if err := files.root.Mkdir(name, 0700); err != nil {
		return err
	}
	defer func() {
		if err := files.root.Remove(name); err != nil {
			result = errors.Join(result, ErrStorageCleanup)
		} else if err := files.ops.syncDir(files.root); err != nil {
			result = errors.Join(result, ErrStorageCleanup)
		}
	}()
	if err := files.ops.syncDir(files.root); err != nil {
		return err
	}
	probe := store
	probe.path = filepath.Join(filepath.Dir(store.path), name, "session.enc")
	probe.secrets.account = name
	return exerciseStorage(probe, func() error {
		if err := removeStorageProbe(probe); err != nil {
			return err
		}
		key, err := probe.secrets.loadKey()
		clear(key)
		if !errors.Is(err, ErrNotFound) {
			return errors.New("storage check could not verify temporary key removal")
		}
		return nil
	})
}
