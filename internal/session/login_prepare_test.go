package session

import (
	"context"
	"errors"
	"testing"
)

type identityStore struct {
	*memoryStore
	identity string
}

func (s *identityStore) StorageIdentity() (string, error) { return s.identity, nil }

func TestLoginSnapshotChecksAllLocalIdentity(t *testing.T) {
	for _, change := range []string{"created", "deleted", "generation", "mid", "invalidated", "storage", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			s := &identityStore{&memoryStore{state: &State{MID: "old", Generation: "generation"}}, "native"}
			if change == "created" {
				s.state = nil
			}
			m := NewManager(s)
			before, err := m.PrepareLogin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "created":
				s.state = &State{MID: "new", Generation: "new"}
			case "deleted":
				s.state = nil
			case "generation":
				s.state.Generation = "new"
			case "mid":
				s.state.MID = "new"
			case "invalidated":
				s.state.Invalidated = true
			case "storage":
				s.identity = "headless"
			}
			err = m.CheckLogin(context.Background(), before)
			if change == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrStorageChanged) {
				t.Fatal("identity change ignored", err)
			}
		})
	}
}
