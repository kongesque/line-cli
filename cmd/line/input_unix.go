//go:build darwin || linux

package main

import (
	"context"
	"io"
	"sync"

	"golang.org/x/sys/unix"
)

type consoleReader struct {
	ctx    context.Context
	fd     int
	mu     sync.Mutex
	closed bool
}

// Poll so cancellation finishes every OS read before process exit. Cleanup
// joins active reads, and no worker can enter a read after cleanup. Descriptor
// flags change only during input and are restored before releasing the lock.
func commandInput(ctx context.Context, fd int) (io.Reader, func()) {
	r := &consoleReader{ctx: ctx, fd: fd}
	return r, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.closed = true
	}
}

func (r *consoleReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	flags, err := unix.FcntlInt(uintptr(r.fd), unix.F_GETFL, 0)
	if err != nil {
		return 0, err
	}
	if err := unix.SetNonblock(r.fd, true); err != nil {
		return 0, err
	}
	defer unix.FcntlInt(uintptr(r.fd), unix.F_SETFL, flags)
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, 50); err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, err
		}
		if fds[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(r.fd, p)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return max(0, n), err
	}
}
