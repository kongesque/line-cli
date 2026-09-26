package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/kongesque/line-cli/internal/cli"
	"github.com/kongesque/line-cli/internal/session"
)

type commandSignals struct {
	ctx     context.Context
	cancel  context.CancelFunc
	code    atomic.Int32
	done    chan struct{}
	signals chan os.Signal
}

func newCommandSignals() *commandSignals {
	ctx, cancel := context.WithCancel(context.Background())
	s := &commandSignals{ctx: ctx, cancel: cancel, done: make(chan struct{}), signals: make(chan os.Signal, 2)}
	signal.Notify(s.signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer close(s.done)
		select {
		case sig := <-s.signals:
			code := int32(130)
			if sig == syscall.SIGTERM {
				code = 143
			}
			s.code.Store(code) // Publish the signal before cancellation wakes readers.
			s.cancel()
		case <-ctx.Done():
		}
	}()
	return s
}

func (s *commandSignals) close() {
	signal.Stop(s.signals)
	s.cancel()
	<-s.done
}

func commandExit(err error, signalCode int, out io.Writer) int {
	// Keep structured login errors: cancellation can follow remote approval or
	// uncertain local persistence, and a generic cancellation would hide that.
	var login *session.LoginError
	if err != nil {
		switch {
		case errors.As(err, &login):
			fmt.Fprintln(out, "line:", err)
		case errors.Is(err, cli.ErrCancelled), errors.Is(err, context.Canceled):
			fmt.Fprintln(out, "Cancelled.")
		default:
			fmt.Fprintln(out, "line:", err)
		}
	} else if signalCode != 0 {
		fmt.Fprintln(out, "Stopped after receiving a signal.")
	}
	if signalCode != 0 {
		return signalCode
	}
	if err == nil || errors.Is(err, cli.ErrCancelled) {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return session.StorageExitCode(err)
}
