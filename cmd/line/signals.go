package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/kongesque/line-cli/internal/cli"
	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
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
	// Let writes return EPIPE so a confirmed mutation can still be reported on
	// stderr. Go otherwise terminates immediately on a broken stdout pipe.
	signal.Ignore(syscall.SIGPIPE)
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
	code := commandExitCode(err, signalCode)
	var machine *cli.MachineError
	if errors.As(err, &machine) {
		// A failed stderr write must not trigger another mutation or print the cause.
		_ = json.NewEncoder(out).Encode(struct {
			Schema   int          `json:"schema"`
			Error    *fault.Error `json:"error"`
			ExitCode int          `json:"exit_code"`
		}{1, machine.Detail, code})
		return code
	}
	// Keep wrapped operation errors: cancellation can follow dispatch, approval or
	// uncertain local persistence, and a generic cancellation would hide that.
	var login *session.LoginError
	var mutation *messaging.MutationError
	var typed *fault.Error
	if err != nil {
		switch {
		case errors.As(err, &login), errors.As(err, &mutation), errors.As(err, &typed):
			fmt.Fprintln(out, "line:", err)
		case errors.Is(err, cli.ErrCancelled), errors.Is(err, context.Canceled):
			fmt.Fprintln(out, "Cancelled.")
		default:
			fmt.Fprintln(out, "line:", err)
		}
	} else if signalCode != 0 {
		fmt.Fprintln(out, "Stopped after receiving a signal.")
	}
	return code
}

func commandExitCode(err error, signalCode int) int {
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
