package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/kongesque/line-cli/internal/events"
	"github.com/kongesque/line-cli/internal/session"
)

func (a *App) watchCommand(args []string) error {
	fs := newFlagSet("watch")
	h := commandHelp{Command: "watch", Usage: []string{"line watch [options]"},
		About:    "Stream live events as one JSON object per line. Starts now on first use and\nresumes from the saved position on later runs. Never sends read receipts.",
		Groups:   []flagGroup{{"Stream", []string{"from-now", "limit", "timeout"}}, {"Output", []string{"json"}}},
		Examples: []string{"line watch", "line watch --limit 10 --timeout 5m"}}
	jsonOutput := fs.Bool("json", true, "write newline-delimited JSON (the watch output format)")
	fromNow := fs.Bool("from-now", false, "discard the saved resume position and start at the current revision")
	limit := fs.Int("limit", 0, "stop after N emitted events; 0 watches continuously")
	timeout := fs.Duration("timeout", 0, "stop after this duration, e.g. 30s; 0 watches continuously")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if fs.NArg() != 0 || *limit < 0 || *timeout < 0 || !*jsonOutput {
		return errors.New("invalid watch arguments; run line watch --help")
	}
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	lock := a.WatchLock
	if lock == nil {
		lock = session.WatchLock
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	fmt.Fprintln(a.Err, "Watching LINE events. Press Ctrl-C to stop.")
	w := &events.Watcher{Manager: a.Manager, Lock: a.Lock, Out: a.Out, Err: a.Err, FromNow: *fromNow, Limit: *limit}
	err = w.Run(ctx)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}
