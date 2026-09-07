package cli

import (
	"context"
	"testing"
	"time"

	"github.com/highesttt/matrix-line-messenger/internal/session"
)

func TestWatchArgumentsBeforeSession(t *testing.T) {
	for _, args := range [][]string{{"watch", "--help"}, {"watch", "--limit", "-1"}, {"watch", "--timeout", "-1s"}, {"watch", "extra"}, {"watch", "--timeout", "invalid"}} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("session opened before validation"); return nil, nil }
		a.WatchLock = a.Lock
		_ = a.Run(args)
	}
}

type watchAPI struct{ session.API }

func (*watchAPI) GetLastOpRevisionContext(context.Context) (int64, error) { return 1, nil }
func (*watchAPI) ListenSSE(ctx context.Context, _ int64, _ func(string, string)) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestWatchTimeoutAndCancellationReleaseWatchLock(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		a, out, _ := testApp(nil)
		a.Manager.NewClient = func(string) session.API { return &watchAPI{} }
		released := false
		a.WatchLock = func() (func(), error) { return func() { released = true }, nil }
		ctx, cancel := context.WithCancel(context.Background())
		a.Context = ctx
		if cancelled {
			cancel()
		}
		start := time.Now()
		err := a.Run([]string{"watch", "--json", "--timeout", "5ms"})
		cancel()
		if err != nil || !released || out.Len() != 0 || time.Since(start) > time.Second {
			t.Fatal("watch failed to stop cleanly", err)
		}
	}
}
