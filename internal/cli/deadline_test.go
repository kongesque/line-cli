package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type deadlineAPI struct {
	session.API
	calls int
}

func (f *deadlineAPI) GetProfileContext(ctx context.Context) (*line.Profile, error) {
	f.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *deadlineAPI) GetMessageBoxesContext(ctx context.Context, _ line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	f.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *deadlineAPI) GetRecentMessagesV2Context(ctx context.Context, _ string, _ int) ([]*line.Message, error) {
	f.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *deadlineAPI) GetAllContactIdsContext(ctx context.Context) ([]string, error) {
	f.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCommandDeadlinesCoverPreparationAndReleaseLock(t *testing.T) {
	cases := [][]string{
		{"whoami"}, {"contacts"}, {"chats"}, {"messages", "Alice"},
		{"send", "Alice", "--text", "hello"},
		{"download", "Alice", "--message", "123", "--output", "-"},
		{"react", "id:u-peer", "--message", "123", "--reaction", "like"},
		{"unsend", "id:u-peer", "--message", "123"},
	}
	for _, args := range cases {
		t.Run(args[0], func(t *testing.T) {
			a, _, _ := testApp(nil)
			f := &deadlineAPI{}
			a.Manager.NewClient = func(string) session.API { return f }
			locked := false
			a.Lock = func() (func(), error) {
				if locked {
					t.Fatal("lock not released")
				}
				locked = true
				return func() { locked = false }, nil
			}
			original := context.Background()
			a.Context = original
			start := time.Now()
			err := a.Run(append(args, "--timeout", "10ms"))
			if !errors.Is(err, context.DeadlineExceeded) || locked || f.calls != 1 || time.Since(start) > time.Second {
				t.Fatalf("err=%v locked=%v calls=%d", err, locked, f.calls)
			}
			if a.Context != original {
				t.Fatal("command context not restored")
			}
		})
	}
}
func TestCancelledCommandsDoNotOpenSession(t *testing.T) {
	for _, args := range [][]string{{"whoami"}, {"contacts"}, {"chats"}, {"messages", "id:u-peer"}, {"send", "id:u-peer", "--text", "hello"}, {"download", "id:u-peer", "--message", "123", "--output", "-"}, {"react", "id:u-peer", "--message", "123", "--reaction", "like"}, {"unsend", "id:u-peer", "--message", "123"}} {
		a, _, _ := testApp(nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		a.Context = ctx
		a.Lock = func() (func(), error) { t.Fatal("opened canceled command session"); return nil, nil }
		if err := a.Run(args); !errors.Is(err, context.Canceled) {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
func TestTimeoutValidationAndHelpDoNotOpenSession(t *testing.T) {
	for _, cmd := range []string{"whoami", "contacts", "chats", "messages", "send", "download", "react", "unsend"} {
		for _, value := range []string{"-1s", "bad"} {
			a, _, _ := testApp(nil)
			a.Lock = func() (func(), error) { t.Fatal("opened session for invalid timeout"); return nil, nil }
			if err := a.Run([]string{cmd, "--timeout", value}); err == nil {
				t.Fatal("accepted invalid timeout")
			}
		}
		a, _, diagnostics := testApp(nil)
		if err := a.Run([]string{cmd, "--help"}); err != nil || !strings.Contains(diagnostics.String(), "-timeout") {
			t.Fatalf("help %s: %v", cmd, err)
		}
	}
}
func TestDeadlineIncludesStdin(t *testing.T) {
	a, _, _ := testApp(nil)
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	a.In = r
	a.Lock = func() (func(), error) { t.Fatal("opened session before input"); return nil, nil }
	if err := a.Run([]string{"send", "id:u-peer", "--stdin", "--timeout", "10ms"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
