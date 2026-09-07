package events

import (
	"context"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/highesttt/matrix-line-messenger/internal/session"
)

type liveAPI struct {
	session.API
	started    chan struct{}
	operations *int
	keepalives *int
}

func (a *liveAPI) ListenSSE(ctx context.Context, revision int64, callback func(string, string)) error {
	select {
	case a.started <- struct{}{}:
	default:
	}
	return a.API.ListenSSE(ctx, revision, func(kind, data string) {
		if kind == "operation" {
			*a.operations++
		}
		if kind == "ping" || kind == "connInfoRevision" {
			*a.keepalives++
		}
		callback(kind, data)
	})
}

// Explicitly opt in: this reads the saved session and updates its watch cursor.
// It does not transmit messages or print/persist incoming event payloads.
func TestLiveWatch(t *testing.T) {
	if os.Getenv("LINE_CLI_LIVE_WATCH") != "1" {
		t.Skip("set LINE_CLI_LIVE_WATCH=1 after signing in")
	}
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(previousLog)
	unlock, err := session.WatchLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	m := session.NewManager(session.KeychainStore{})
	original := m.NewClient
	started := make(chan struct{}, 1)
	operations, keepalives := 0, 0
	m.NewClient = func(token string) session.API {
		return &liveAPI{API: original(token), started: started, operations: &operations, keepalives: &keepalives}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	w := &Watcher{Manager: m, Lock: session.Lock, Out: io.Discard, Err: io.Discard}
	done := make(chan error, 1)
	stopped := make(chan struct{})
	start := time.Now()
	go func() { defer close(stopped); done <- w.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	select {
	case <-started:
		t.Logf("stream started after %.1fs", time.Since(start).Seconds())
	case err := <-done:
		t.Fatalf("watch stopped before opening stream: %v", err)
	case <-ctx.Done():
		t.Fatal("stream did not start before deadline")
	}
	// Let SSE reach the server, then prove that an independent command can
	// hold the session lock and read a profile while the stream is open.
	time.Sleep(time.Second)
	var probeErr error
	for attempt := 0; attempt < 20; attempt++ {
		var release func()
		release, probeErr = session.Lock()
		if probeErr == nil {
			probeErr = m.Do(func(api session.API) error { _, err := api.GetProfileContext(ctx); return err })
			release()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Cancellation precedes checking shared counters or reporting probe errors.
	cancel()
	<-done
	if probeErr != nil {
		t.Fatalf("concurrent profile query failed: %v", probeErr)
	}
	t.Logf("concurrent profile succeeded; operation frames=%d, keepalives=%d", operations, keepalives)
}
