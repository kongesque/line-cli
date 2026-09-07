// Package events consumes LINE operations without sending messages or read receipts.
package events

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/highesttt/matrix-line-messenger/internal/messaging"
	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

type Event struct {
	Event    string             `json:"event"`
	Revision string             `json:"revision"`
	Type     int                `json:"type,omitempty"`
	ChatID   string             `json:"chat_id,omitempty"`
	Message  *messaging.Message `json:"message,omitempty"`
}

type Watcher struct {
	Manager *session.Manager
	Lock    func() (func(), error)
	Out     io.Writer
	Err     io.Writer
	FromNow bool
	Limit   int
	// Hooks keep reconnection and cancellation tests independent of wall time.
	ProbeInterval time.Duration
	Wait          func(context.Context, time.Duration) error
}

func (w *Watcher) locked(ctx context.Context, fn func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		unlock, err := w.Lock()
		if errors.Is(err, session.ErrBusy) {
			if err := w.Wait(ctx, 100*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		defer unlock()
		return fn()
	}
}

func (w *Watcher) state(generation string) (*session.State, error) {
	s, err := w.Manager.Store.Load()
	if err != nil {
		return nil, err
	}
	if s.Invalidated {
		return nil, errors.New("LINE session was logged out; run line login")
	}
	if generation != "" && s.Generation != generation {
		return nil, errors.New("LINE login changed; restart line watch")
	}
	return s, nil
}

// Run uses short session locks; the caller holds a separate singleton watch lock.
// Output precedes checkpointing. A crash in between can replay the last event.
func (w *Watcher) Run(ctx context.Context) error {
	if w.Wait == nil {
		w.Wait = wait
	}
	if w.ProbeInterval <= 0 {
		w.ProbeInterval = 30 * time.Second
	}
	if w.Err == nil {
		w.Err = io.Discard
	}
	var generation string
	var revision int64
	var decoder *messaging.Client
	encoder := json.NewEncoder(w.Out)
	backoff := time.Second
	count := 0
	profileProbe := false
	for {
		var api session.API
		var streamToken string
		err := w.locked(ctx, func() error {
			s, err := w.state(generation)
			if err != nil {
				return err
			}
			if s.Generation == "" {
				s.Generation = rand.Text()
				if err := w.Manager.Store.Save(s); err != nil {
					return err
				}
			}
			generation = s.Generation
			// A Talk probe exposes forced logout even if SSE remains connected or
			// returns only HTTP 401. Manager persists refreshed tokens before use.
			var latest int64
			if err := w.Manager.Do(func(client session.API) (err error) {
				if profileProbe {
					if _, err = client.GetProfileContext(ctx); err != nil {
						return err
					}
				}
				latest, err = client.GetLastOpRevisionContext(ctx)
				return
			}); err != nil {
				return err
			}
			if latest < 0 {
				return errors.New("LINE returned an invalid operation revision")
			}
			s, err = w.state(generation)
			if err != nil {
				return err
			}
			if s.WatchRevision == nil || w.FromNow {
				s.WatchRevision = &latest
				if err := w.Manager.Store.Save(s); err != nil {
					return err
				}
				w.FromNow = false
			}
			revision = *s.WatchRevision
			if revision < 0 {
				return errors.New("saved watch revision is invalid; use line watch --from-now")
			}
			api = w.Manager.NewClient(s.AccessToken)
			streamToken = s.AccessToken
			if decoder == nil {
				decoder, err = messaging.New(w.Manager)
			}
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var remote *session.RemoteError
			if !errors.As(err, &remote) || line.IsAuthError(session.ProtocolError(err)) {
				return err
			}
			if err := w.retry(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		profileProbe = false
		streamCtx, cancel := context.WithTimeout(ctx, w.ProbeInterval)
		var callbackErr error
		var eventError error
		received := false
		streamErr := api.ListenSSE(streamCtx, revision, func(kind, data string) {
			if callbackErr != nil || eventError != nil || (w.Limit > 0 && count >= w.Limit) {
				return
			}
			if kind == "ping" || kind == "connInfoRevision" {
				return
			}
			if kind == "error" {
				eventError = errors.New(data)
				cancel()
				return
			}
			callbackErr = w.locked(ctx, func() error {
				if _, err := w.state(generation); err != nil {
					return err
				}
				event, next, err := decodeEvent(kind, data, revision, decoder)
				if err != nil {
					return err
				}
				if event == nil {
					return nil
				}
				// Decryption may refresh tokens or invalidate the session. Reload
				// after it so checkpointing never overwrites those changes.
				s, err := w.state(generation)
				if err != nil {
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := encoder.Encode(event); err != nil {
					return errors.New("could not write watch output; the event will replay on restart")
				}
				s.WatchRevision = &next
				if err := w.Manager.Store.Save(s); err != nil {
					return err
				}
				revision = next
				count++
				received = true
				return nil
			})
			if callbackErr != nil || (w.Limit > 0 && count >= w.Limit) {
				cancel()
			}
		})
		probeDue := errors.Is(streamCtx.Err(), context.DeadlineExceeded)
		cancel()
		if callbackErr != nil {
			return callbackErr
		}
		if w.Limit > 0 && count >= w.Limit {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if eventError != nil {
			streamErr = eventError
		}
		if line.IsLoggedOut(streamErr) {
			err := w.locked(ctx, func() error {
				s, err := w.state(generation)
				if err != nil {
					return err
				}
				// Another command may have rotated credentials during the stream.
				// An old transport must never invalidate the newer token.
				if s.AccessToken != streamToken {
					return nil
				}
				return w.Manager.MarkLoggedOut()
			})
			if err != nil {
				return err
			}
			continue
		}
		profileProbe = line.IsAuthError(streamErr)
		if eventError != nil && !profileProbe {
			return errors.New("LINE reported a stream error; restart line watch")
		}
		if probeDue {
			backoff = time.Second
			continue
		}
		if received {
			backoff = time.Second
		}
		if err := w.retry(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (w *Watcher) retry(ctx context.Context, delay time.Duration) error {
	fmt.Fprintf(w.Err, "LINE stream disconnected; reconnecting in %s…\n", delay)
	return w.Wait(ctx, delay)
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func decodeEvent(kind, data string, current int64, decoder *messaging.Client) (*Event, int64, error) {
	if kind == "fullSync" {
		var payload struct {
			NextRevision json.Number `json:"nextRevision"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return nil, current, errors.New("invalid LINE fullSync event; resume position was not advanced")
		}
		next, err := payload.NextRevision.Int64()
		if err != nil || next < 0 {
			return nil, current, errors.New("invalid LINE fullSync revision; resume position was not advanced")
		}
		if next <= current {
			return nil, current, nil
		}
		return &Event{Event: "resync_required", Revision: strconv.FormatInt(next, 10)}, next, nil
	}
	if kind != "operation" {
		return nil, current, errors.New("unsupported LINE stream event; resume position was not advanced")
	}
	var op line.Operation
	if err := json.Unmarshal([]byte(data), &op); err != nil {
		return nil, current, errors.New("invalid LINE operation event; resume position was not advanced")
	}
	next, err := op.Revision.Int64()
	if err != nil || next < 0 {
		return nil, current, errors.New("invalid LINE operation revision; resume position was not advanced")
	}
	if next <= current {
		return nil, current, nil
	}
	event := &Event{Event: "operation", Revision: strconv.FormatInt(next, 10), Type: op.Type}
	if (op.Type == 25 || op.Type == 26) && op.Message != nil {
		chat := op.Message.To
		if op.Type == 26 && op.Message.ToType == 0 {
			chat = op.Message.From
		}
		if messaging.ValidateChatID(chat) != nil {
			return nil, current, errors.New("LINE message has an invalid chat ID; resume position was not advanced")
		}
		msg := decoder.Decode(chat, op.Message)
		event.Event, event.ChatID, event.Message = "message", chat, &msg
	}
	return event, next, nil
}
