package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/highesttt/matrix-line-messenger/internal/events"
	"github.com/highesttt/matrix-line-messenger/internal/session"
)

func (a *App) watchCommand(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line watch [--json] [--from-now] [--limit N] [--timeout DURATION]\nWrites one JSON event per line. Starts now on first use; resumes on later runs.")
		fs.PrintDefaults()
	}
	jsonOutput := fs.Bool("json", true, "write newline-delimited JSON (the watch output format)")
	fromNow := fs.Bool("from-now", false, "discard the saved resume position and start at the current revision")
	limit := fs.Int("limit", 0, "stop after N emitted events; 0 watches continuously")
	timeout := fs.Duration("timeout", 0, "stop after this duration, e.g. 30s; 0 watches continuously")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line watch --help")
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
