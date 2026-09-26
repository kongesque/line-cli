package session

import (
	"context"
	"errors"
)

// LoginSnapshot records local identity before prompts. It contains no credentials.
// Call PrepareLogin under the command lock, release the lock for local input,
// then reacquire it and call CheckLogin before LoginContext or LoginQR. Hold the
// lock through authentication and saving. Absence is part of the snapshot too.
type LoginSnapshot struct {
	exists          bool
	mid, generation string
	invalidated     bool
	storageIdentity string
}

func (m *Manager) PrepareLogin(ctx context.Context) (*LoginSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.PrepareStorage(); err != nil {
		return nil, err
	}
	return m.loginSnapshot()
}

func (m *Manager) CheckLogin(ctx context.Context, before *LoginSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if before == nil {
		return errors.New("login requires a local session snapshot")
	}
	after, err := m.loginSnapshot()
	if err != nil {
		return err
	}
	if *after != *before {
		return ErrStorageChanged
	}
	return nil
}

func (m *Manager) loginSnapshot() (*LoginSnapshot, error) {
	snapshot := &LoginSnapshot{}
	if selector, ok := m.Store.(interface{ StorageIdentity() (string, error) }); ok {
		var err error
		snapshot.storageIdentity, err = selector.StorageIdentity()
		if err != nil {
			return nil, err
		}
	}
	s, err := m.Store.Load()
	if errors.Is(err, ErrNotFound) {
		return snapshot, nil
	}
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("saved session is invalid")
	}
	snapshot.exists, snapshot.mid, snapshot.generation, snapshot.invalidated = true, s.MID, s.Generation, s.Invalidated
	return snapshot, nil
}
