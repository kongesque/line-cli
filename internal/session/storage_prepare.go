package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
)

// StoragePreparer is separate from Store's persistence boundary. Call it while
// holding the session lock, before collecting a password or contacting LINE.
type StoragePreparer interface{ Prepare() error }

type NativeStorageController struct{}

func (NativeStorageController) Prepare() error { return prepareNativeStorage() }

func (KeychainStore) Prepare() error { return (NativeStorageController{}).Prepare() }

func checkSavedStorage(store Store) error {
	state, err := store.Load()
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == nil {
		return errors.New("saved session is invalid")
	}
	return nil
}

// The caller supplies a newly named, isolated native item/file. Even a failed
// save can leave an item behind, so cleanup always runs and must be verified.
func exerciseStorage(store Store, cleanup func() error) (result error) {
	defer func() {
		if err := cleanup(); err != nil {
			result = errors.Join(result, fmt.Errorf("%w: %w", ErrStorageCleanup, err))
		}
	}()
	state := &State{Version: 1, MID: "storage-probe", AccessToken: rand.Text(),
		Generation: rand.Text(), ExportedKeys: map[string]string{"probe": rand.Text()}}
	for i := 0; i < 2; i++ {
		state.LastReqSeq = int64(i)
		if err := store.Save(state); err != nil {
			return err
		}
		got, err := store.Load()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, state) {
			return errors.New("storage check could not verify saved data")
		}
	}
	return nil
}

func removeStorageProbe(store Store) error {
	if err := store.Delete(); err != nil {
		return err
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotFound) {
		return errors.New("storage check could not verify removal")
	}
	return nil
}
