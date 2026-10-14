package session

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestDeleteRetainsWrappingKeyUntilRemovalIsDurable(t *testing.T) {
	s, service := syntheticFileStore(t)
	if err := s.Save(&State{Version: 1, MID: "test", AccessToken: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	key := append([]byte(nil), service.values["default"]...)
	ops := defaultFileOperations()
	ops.syncDir = func(*os.Root) error { return errors.New("sync failure") }
	s.ops = &ops
	if err := s.Delete(); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal(err)
	}
	if !bytes.Equal(key, service.values["default"]) {
		t.Fatal("deleted key after uncertain removal")
	}
	s.ops = nil
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if len(service.values) != 0 {
		t.Fatal("retry did not finish key cleanup")
	}
}

type probeMemoryStore struct {
	state       *State
	deletes     int
	corrupt     bool
	deleteError bool
	saveError   bool
}

func (s *probeMemoryStore) Load() (*State, error) {
	if s.state == nil {
		return nil, ErrNotFound
	}
	copy := *s.state
	if s.corrupt {
		copy.AccessToken = "changed"
	}
	return &copy, nil
}
func (s *probeMemoryStore) Save(state *State) error {
	copy := *state
	s.state = &copy
	if s.saveError {
		return errors.New("save failed after creating the item")
	}
	return nil
}
func (s *probeMemoryStore) Delete() error {
	s.deletes++
	if s.deleteError {
		return errors.New("cleanup unavailable")
	}
	s.state = nil
	return nil
}

func TestStorageProbeCleansUpPartialSave(t *testing.T) {
	s := &probeMemoryStore{saveError: true}
	if err := exerciseStorage(s, func() error { return removeStorageProbe(s) }); err == nil {
		t.Fatal("save failure ignored")
	}
	if s.deletes != 1 || s.state != nil {
		t.Fatal("failed save left a probe behind")
	}
}

func TestStorageProbeVerifiesRoundtripAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		corrupt, cleanupError bool
	}{{"success", false, false}, {"corrupt", true, false}, {"cleanup", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &probeMemoryStore{corrupt: tc.corrupt, deleteError: tc.cleanupError}
			err := exerciseStorage(s, func() error { return removeStorageProbe(s) })
			if (err != nil) != (tc.corrupt || tc.cleanupError) {
				t.Fatal(err)
			}
			if s.deletes != 1 {
				t.Fatal("cleanup not attempted exactly once")
			}
			if tc.cleanupError && !errors.Is(err, ErrStorageCleanup) {
				t.Fatal("cleanup failure lost", err)
			}
			if !tc.cleanupError && s.state != nil {
				t.Fatal("probe survived cleanup")
			}
		})
	}
}

type storagePreparerFunc func() error

func (f storagePreparerFunc) Prepare() error { return f() }

func TestManagerRejectsStorageFailureBeforeCreatingClient(t *testing.T) {
	m := NewManager(&probeMemoryStore{})
	m.Storage = storagePreparerFunc(func() error { return ErrStorageCleanup })
	m.NewClient = func(string) API { t.Fatal("storage failure contacted LINE"); return nil }
	if _, err := m.Login("synthetic", "synthetic", nil); !errors.Is(err, ErrStorageCleanup) {
		t.Fatal(err)
	}
	if NewManager(KeychainStore{}).Storage == nil {
		t.Fatal("native manager has no preflight")
	}
}
