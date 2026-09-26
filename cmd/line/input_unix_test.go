//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConsoleCancellationRestoresDescriptor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(r.Fd())
	before, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in, cleanup := commandInput(ctx, fd)
	defer cleanup()
	result := make(chan error, 1)
	go func() { var b [1]byte; _, err := in.Read(b[:]); result <- err }()
	// Wait until the read is active, so this exercises in-flight cancellation.
	deadline := time.After(time.Second)
	for {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			t.Fatal(err)
		}
		if flags&unix.O_NONBLOCK != 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("read did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not cancel")
	}
	cleanup()
	after, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || after != before {
		t.Fatal("stdin flags were not restored", err)
	}
}
