package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

type memoryStore struct {
	state        *session.State
	failRevision int64
}

func (s *memoryStore) Load() (*session.State, error) {
	if s.state == nil {
		return nil, session.ErrNotFound
	}
	b, _ := json.Marshal(s.state)
	var copy session.State
	err := json.Unmarshal(b, &copy)
	return &copy, err
}
func (s *memoryStore) Save(state *session.State) error {
	if s.failRevision != 0 && state.WatchRevision != nil && *state.WatchRevision == s.failRevision {
		return errors.New("checkpoint failed")
	}
	b, _ := json.Marshal(state)
	return json.Unmarshal(b, &s.state)
}
func (s *memoryStore) Delete() error { s.state = nil; return nil }

type frame struct{ kind, data string }
type stream struct {
	frames []frame
	err    error
}
type fakeAPI struct {
	session.API
	t            *testing.T
	locked       *bool
	streams      []stream
	revisions    []int64
	latest       int64
	probeErrors  []error
	probes       int
	profiles     int
	profileError error
	refreshes    int
	onListen     func(context.Context, func(string, string)) error
}

func (f *fakeAPI) GetLastOpRevisionContext(context.Context) (int64, error) {
	if !*f.locked {
		f.t.Fatal("probe without session lock")
	}
	f.probes++
	if len(f.probeErrors) > 0 {
		err := f.probeErrors[0]
		f.probeErrors = f.probeErrors[1:]
		return f.latest, err
	}
	return f.latest, nil
}
func (f *fakeAPI) GetProfileContext(context.Context) (*line.Profile, error) {
	f.profiles++
	return &line.Profile{}, f.profileError
}
func (f *fakeAPI) RefreshAccessToken(string) (*line.TokenV3IssueResult, error) {
	f.refreshes++
	return &line.TokenV3IssueResult{AccessToken: "rotated", RefreshToken: "refresh-new", DurationUntilRefreshSec: "3600"}, nil
}
func (f *fakeAPI) ListenSSE(ctx context.Context, rev int64, callback func(string, string)) error {
	if *f.locked {
		f.t.Fatal("watch held the session lock while listening")
	}
	f.revisions = append(f.revisions, rev)
	if f.onListen != nil {
		return f.onListen(ctx, callback)
	}
	if len(f.streams) == 0 {
		f.t.Fatal("unexpected reconnect")
		return nil
	}
	s := f.streams[0]
	f.streams = f.streams[1:]
	for _, event := range s.frames {
		callback(event.kind, event.data)
	}
	return s.err
}

func setup(t *testing.T) (*Watcher, *fakeAPI, *memoryStore, *bytes.Buffer) {
	t.Helper()
	locked := false
	f := &fakeAPI{t: t, locked: &locked, latest: 10}
	s := &memoryStore{state: &session.State{AccessToken: "secret", MID: "u-self", NoE2EE: true}}
	m := session.NewManager(s)
	m.NewClient = func(string) session.API { return f }
	out := new(bytes.Buffer)
	w := &Watcher{Manager: m, Out: out, Err: new(bytes.Buffer), Limit: 1, Wait: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, Lock: func() (func(), error) {
		if locked {
			t.Fatal("nested session lock")
		}
		locked = true
		return func() { locked = false }, nil
	}}
	return w, f, s, out
}

const incoming = `{"revision":"11","type":26,"message":{"id":"msg-1","from":"u-peer","to":"u-self","toType":0,"text":"hello"}}`
const outgoing = `{"revision":"12","type":25,"message":{"id":"msg-2","from":"u-self","to":"c-group","toType":2,"text":"world"}}`

func outputEvents(t *testing.T, out *bytes.Buffer) []Event {
	t.Helper()
	var result []Event
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		var event Event
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, event)
	}
}

func TestWatchSeedsResumesDeduplicatesAndDecodesMessages(t *testing.T) {
	w, f, s, out := setup(t)
	w.Limit = 2
	f.streams = []stream{
		{frames: []frame{{"ping", `{}`}, {"connInfoRevision", `{}`}, {"operation", incoming}}, err: io.EOF},
		{frames: []frame{{"operation", incoming}, {"operation", outgoing}}},
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := outputEvents(t, out)
	if !reflect.DeepEqual(f.revisions, []int64{10, 11}) || len(events) != 2 || *s.state.WatchRevision != 12 {
		t.Fatal("bad checkpoint or replay suppression")
	}
	if events[0].ChatID != "u-peer" || events[1].ChatID != "c-group" || events[0].Message.Text != "hello" {
		t.Fatal("incorrect message routing/decoding")
	}
	f.latest = 999 // Auth probes must not skip pending events on restart.
	f.streams = []stream{{frames: []frame{{"operation", `{"revision":"13","type":1}`}}}}
	w.Limit = 1
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.revisions[2] != 12 || *s.state.WatchRevision != 13 {
		t.Fatal("restart skipped to latest server revision")
	}
}

func TestWatchFullSyncIsExplicitGap(t *testing.T) {
	w, f, s, out := setup(t)
	f.streams = []stream{{frames: []frame{{"fullSync", `{"nextRevision":"9007199254740993"}`}}}}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := outputEvents(t, out)[0]
	if event.Event != "resync_required" || event.Revision != "9007199254740993" || *s.state.WatchRevision != 9007199254740993 {
		t.Fatal("gap not exposed or revision lost precision")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWatchDoesNotCheckpointFailedOutputOrMalformedData(t *testing.T) {
	for _, tc := range []struct {
		name, kind, data string
		broken           bool
	}{
		{"output", "operation", incoming, true},
		{"json", "operation", `{"secret":"token"`, false},
		{"revision", "operation", `{"revision":"oops","type":26}`, false},
		{"missing_revision", "operation", `{}`, false},
		{"fullsync", "fullSync", `{"nextRevision":"oops"}`, false},
		{"unknown", "unexpected", `secret-token`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, f, s, out := setup(t)
			if tc.broken {
				w.Out = brokenWriter{}
			}
			f.streams = []stream{{frames: []frame{{tc.kind, tc.data}}}}
			err := w.Run(context.Background())
			if err == nil || strings.Contains(err.Error(), "secret") || *s.state.WatchRevision != 10 || out.Len() != 0 {
				t.Fatal("failed event was consumed or payload exposed", err)
			}
		})
	}
}

func TestWatchCheckpointFailureReplaysAfterRestart(t *testing.T) {
	w, f, s, out := setup(t)
	s.failRevision = 11
	f.streams = []stream{{frames: []frame{{"operation", incoming}}}}
	if err := w.Run(context.Background()); err == nil {
		t.Fatal("save failure swallowed")
	}
	if len(outputEvents(t, out)) != 1 || *s.state.WatchRevision != 10 {
		t.Fatal("incorrect output/checkpoint ordering")
	}
	s.failRevision = 0
	f.streams = []stream{{frames: []frame{{"operation", incoming}}}}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(outputEvents(t, out)) != 2 || f.revisions[1] != 10 {
		t.Fatal("uncommitted event not replayed")
	}
}

func TestWatchFromNowAndBackoffCancellation(t *testing.T) {
	w, f, s, _ := setup(t)
	old := int64(1)
	s.state.WatchRevision = &old
	w.FromNow = true
	f.streams = []stream{{err: io.EOF}, {err: errors.New("secret network body")}, {err: io.EOF}}
	var delays []time.Duration
	w.Wait = func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		if len(delays) == 3 {
			return context.Canceled
		}
		return nil
	}
	if err := w.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}) || f.revisions[0] != 10 || strings.Contains(w.Err.(*bytes.Buffer).String(), "secret") {
		t.Fatal("invalid backoff, reset, or diagnostics")
	}
}

func TestWatchCancellationReleasesSessionAndStream(t *testing.T) {
	w, f, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.onListen = func(streamCtx context.Context, _ func(string, string)) error {
		cancel()
		<-streamCtx.Done()
		return streamCtx.Err()
	}
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if *f.locked {
		t.Fatal("session lock leaked")
	}
}

func TestWatchSessionReplacementAndLogoutStopBeforeOutput(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "relogin", true: "logout"}[remove], func(t *testing.T) {
			w, f, s, out := setup(t)
			f.onListen = func(_ context.Context, cb func(string, string)) error {
				if remove {
					s.state = nil
				} else {
					s.state.Generation = "new-login"
				}
				cb("operation", incoming)
				return io.EOF
			}
			if err := w.Run(context.Background()); err == nil || out.Len() != 0 {
				t.Fatal("old watcher continued after session change")
			}
			if remove && s.state != nil {
				t.Fatal("deleted session resurrected")
			}
		})
	}
}

func TestWatchForcedLogoutIsPersistentAndNotRefreshed(t *testing.T) {
	w, f, s, out := setup(t)
	s.state.RefreshToken = "refresh"
	f.streams = []stream{{err: errors.New("SSE error: 401")}}
	f.profileError = errors.New("V3_TOKEN_CLIENT_LOGGED_OUT secret body")
	err := w.Run(context.Background())
	if err == nil || !s.state.Invalidated || f.refreshes != 0 || f.profiles != 1 || out.Len() != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatal("forced logout was retried or exposed", err)
	}
}

func TestWatchRefreshesAndUsesRotatedToken(t *testing.T) {
	w, f, s, _ := setup(t)
	s.state.RefreshToken = "refresh"
	f.probeErrors = []error{errors.New(`{"code":119}`), nil}
	var tokens []string
	w.Manager.NewClient = func(token string) session.API { tokens = append(tokens, token); return f }
	f.streams = []stream{{frames: []frame{{"operation", incoming}}}}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 1 || s.state.AccessToken != "rotated" || tokens[len(tokens)-1] != "rotated" || *s.state.WatchRevision != 11 {
		t.Fatal("rotated session overwritten by checkpoint")
	}
}

func TestWatchPeriodicProbeDetectsQuietForcedLogout(t *testing.T) {
	w, f, s, _ := setup(t)
	w.ProbeInterval = time.Millisecond
	f.probeErrors = []error{nil, errors.New("V3_TOKEN_CLIENT_LOGGED_OUT")}
	f.onListen = func(ctx context.Context, _ func(string, string)) error { <-ctx.Done(); return ctx.Err() }
	if err := w.Run(context.Background()); err == nil || !s.state.Invalidated || f.probes != 2 {
		t.Fatal("quiet stream hid forced logout", err)
	}
}

func TestWatchEncryptedFailureDoesNotExposeChunks(t *testing.T) {
	w, f, _, out := setup(t)
	f.streams = []stream{{frames: []frame{{"operation", `{"revision":"11","type":26,"message":{"id":"encrypted","from":"u-peer","to":"u-self","toType":0,"text":"fallback-secret","chunks":["secret-chunk"]}}`}}}}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret") || outputEvents(t, out)[0].Message.Status != "decryption_failed" {
		t.Fatal("ciphertext/fallback exposed")
	}
}

func TestWatchStaleStreamCannotInvalidateRotatedCredentials(t *testing.T) {
	w, f, s, out := setup(t)
	f.onListen = func(_ context.Context, cb func(string, string)) error {
		if len(f.revisions) == 1 {
			s.state.AccessToken = "newer-token"
			return errors.New("V3_TOKEN_CLIENT_LOGGED_OUT")
		}
		cb("operation", incoming)
		return nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.state.Invalidated || s.state.AccessToken != "newer-token" || len(outputEvents(t, out)) != 1 {
		t.Fatal("old stream invalidated current credentials")
	}
}

func TestWatchLoggedOutStreamDoesNotAttemptProactiveRefresh(t *testing.T) {
	w, f, s, _ := setup(t)
	s.state.RefreshToken = "refresh"
	f.onListen = func(_ context.Context, _ func(string, string)) error {
		s.state.RefreshAt = time.Unix(1, 0)
		return errors.New("V3_TOKEN_CLIENT_LOGGED_OUT")
	}
	if err := w.Run(context.Background()); err == nil || !s.state.Invalidated || f.refreshes != 0 {
		t.Fatal("logout incorrectly triggered refresh", err)
	}
}

func TestWatchWaitsForBusySessionAndPreservesConcurrentSequence(t *testing.T) {
	w, f, s, _ := setup(t)
	lock := w.Lock
	busy := true
	w.Lock = func() (func(), error) {
		if busy {
			busy = false
			return nil, session.ErrBusy
		}
		return lock()
	}
	f.onListen = func(_ context.Context, cb func(string, string)) error {
		s.state.LastReqSeq = 12345 // A separate send completed while SSE was open.
		cb("operation", incoming)
		return nil
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.state.LastReqSeq != 12345 || *s.state.WatchRevision != 11 {
		t.Fatal("checkpoint clobbered concurrent session update")
	}
}
