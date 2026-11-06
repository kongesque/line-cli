package session

import (
	"context"
	"errors"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
)

// guardStore fails the test if read-only guards touch credentials first.
type guardStore struct {
	memoryStore
	t     *testing.T
	armed bool
}

func (s *guardStore) Load() (*State, error) {
	if s.armed {
		s.t.Fatal("read-only guard loaded credentials")
	}
	return s.memoryStore.Load()
}

func (s *guardStore) Save(state *State) error {
	if s.armed {
		s.t.Fatal("read-only guard saved credentials")
	}
	return s.memoryStore.Save(state)
}

func TestReadOnlyManagerRefusesMutationsBeforeCredentials(t *testing.T) {
	store := &guardStore{memoryStore: memoryStore{state: &State{AccessToken: "synthetic", LastReqSeq: 7}}, t: t}
	m := NewManager(store)
	m.NewClient = func(string) API { t.Fatal("read-only guard created a client"); return nil }
	m.ReadOnly = true
	store.armed = true

	err := m.MutateContext(context.Background(), func(API) error { t.Fatal("mutation ran"); return nil })
	var attempt *MutationAttemptError
	var typed *fault.Error
	if !errors.As(err, &attempt) || attempt.Attempted || attempt.Outcome != fault.NotAttempted ||
		!errors.Is(err, ErrReadOnly) || !errors.As(err, &typed) || typed.Code != "read_only" || typed.Stage != "preparation" {
		t.Fatalf("mutation guard: %+v", err)
	}
	if seq, err := m.ReserveSequence(); seq != 0 || !errors.Is(err, ErrReadOnly) {
		t.Fatalf("sequence guard: %d %v", seq, err)
	}
	if _, err := m.LoginContext(context.Background(), "you@example.com", "synthetic", func(string, bool) error {
		t.Fatal("login prompted")
		return nil
	}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("email login guard: %v", err)
	}
	if _, err := m.LoginQR(context.Background(), func(QRLoginEvent) error { t.Fatal("QR login started"); return nil }); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("QR login guard: %v", err)
	}
	if store.memoryStore.state.LastReqSeq != 7 {
		t.Fatal("sequence changed")
	}
}

func TestReadOnlyManagerStillAllowsReads(t *testing.T) {
	m := testManager(&memoryStore{state: &State{AccessToken: "synthetic"}}, &fakeAPI{})
	m.ReadOnly = true
	calls := 0
	if err := m.DoContext(context.Background(), func(API) error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("read refused: %v", err)
	}
}
