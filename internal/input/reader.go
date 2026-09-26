// Package input provides cancellable reads for command-owned input streams.
package input

import (
	"context"
	"io"
)

type Reader struct {
	ctx    context.Context
	source io.Reader
}

func NewReader(ctx context.Context, source io.Reader) *Reader {
	return &Reader{ctx: ctx, source: source}
}

// Read allows the command to unwind even when an OS console read cannot be
// interrupted. At most one source read remains blocked after cancellation, until
// input arrives or the process exits. It owns a private buffer and never writes
// to the caller's memory or changes terminal state after Read returns. A Reader
// is for sequential use and must not be reused with another command/context.
func (r *Reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.ctx.Done() == nil {
		return r.source.Read(p)
	}
	type result struct {
		data []byte
		err  error
	}
	ready := make(chan result)
	go func() {
		buffer := make([]byte, len(p))
		n, err := r.source.Read(buffer)
		select {
		case ready <- result{buffer[:n], err}:
		case <-r.ctx.Done():
			clear(buffer)
		}
	}()
	select {
	case res := <-ready:
		defer clear(res.data)
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		return copy(p, res.data), res.err
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}
