package session_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/events"
	"github.com/kongesque/line-cli/internal/session"
)

type migrationWatchAPI struct {
	session.API
	onListen func(func(string, string)) error
}

func (*migrationWatchAPI) GetLastOpRevisionContext(context.Context) (int64, error) { return 172, nil }
func (a *migrationWatchAPI) ListenSSE(_ context.Context, _ int64, callback func(string, string)) error {
	return a.onListen(callback)
}

func TestWatcherAcrossMigrationAndLogout(t *testing.T) {
	for _, logout := range []bool{false, true} {
		t.Run(map[bool]string{false: "checkpoint", true: "logout"}[logout], func(t *testing.T) {
			rev := int64(172)
			store, migrate := session.NewMigrationFixtureForTest(t, &session.State{Version: 1, MID: "synthetic-self", AccessToken: "synthetic-token", Generation: "same-login", WatchRevision: &rev, LastReqSeq: 73, NoE2EE: true})
			manager := session.NewManager(store)
			manager.Now = func() time.Time { return time.UnixMilli(1) }
			locked := false
			lock := func() (func(), error) {
				if locked {
					t.Fatal("nested lock")
				}
				locked = true
				return func() { locked = false }, nil
			}
			api := &migrationWatchAPI{}
			manager.NewClient = func(string) session.API { return api }
			api.onListen = func(callback func(string, string)) error {
				if locked {
					t.Fatal("watch held lock during listen")
				}
				u, _ := lock()
				if err := migrate(); err != nil {
					t.Fatal(err)
				}
				if logout {
					if err := store.Delete(); err != nil {
						t.Fatal(err)
					}
				} else {
					if seq, err := manager.ReserveSequence(); err != nil || seq != 74 {
						t.Fatal("sequence not preserved", err)
					}
					state, err := store.Load()
					if err != nil {
						t.Fatal(err)
					}
					state.AccessToken = "rotated-during-stream"
					if err := store.Save(state); err != nil {
						t.Fatal(err)
					}
				}
				u()
				callback("operation", `{"revision":"173","type":1}`)
				return nil
			}
			out := new(bytes.Buffer)
			watcher := &events.Watcher{Manager: manager, Lock: lock, Out: out, Err: new(bytes.Buffer), Limit: 1}
			err := watcher.Run(context.Background())
			if logout {
				if !errors.Is(err, session.ErrNotFound) || out.Len() != 0 {
					t.Fatal("logout did not stop watcher", err)
				}
				if _, err := store.Load(); !errors.Is(err, session.ErrNotFound) {
					t.Fatal("watch resurrected logout", err)
				}
			} else {
				if err != nil || out.Len() == 0 {
					t.Fatal("watch failed across migration", err)
				}
				state, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				if state.Generation != "same-login" || state.AccessToken != "rotated-during-stream" || state.LastReqSeq != 74 || *state.WatchRevision != 173 {
					t.Fatal("checkpoint overwrote current migrated state")
				}
			}
			if locked {
				t.Fatal("lock leaked")
			}
		})
	}
}
