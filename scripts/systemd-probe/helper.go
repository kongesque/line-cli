package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

var (
	errOutput  = errors.New("helper output limit exceeded")
	errTimeout = errors.New("helper timed out")
	errHelper  = errors.New("helper failed")
)

type limitedBuffer struct {
	// Do not embed bytes.Buffer: its promoted ReadFrom would let io.Copy in
	// os/exec bypass our Write limit.
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.exceeded = true
		return 0, errOutput
	}
	return b.buffer.Write(p)
}

type helper struct {
	path    string
	timeout time.Duration
	// Test-only prefix supports fake child processes. No shell is involved.
	prefix []string
}

func (h helper) run(args []string, input []byte, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.path, append(append([]string{}, h.prefix...), args...)...)
	// No inherited D-Bus session, credential directory, helper debug flags,
	// Polkit-agent configuration, or secret-valued environment variables.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_LOG_LEVEL=err", "SYSTEMD_PAGER=cat"}
	cmd.Stdin = bytes.NewReader(input)
	stdout := &limitedBuffer{limit: limit}
	stderr := &limitedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Also bound inherited-pipe waits if a child unexpectedly spawns descendants.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	defer clear(stderr.Bytes())
	if err != nil {
		clear(stdout.Bytes())
		// A producer may receive SIGPIPE after our bounded writer rejects data;
		// os/exec can then report its exit status instead of the copy error.
		if stdout.exceeded || stderr.exceeded {
			return nil, errOutput
		}
		if ctx.Err() != nil {
			return nil, errTimeout
		}
		if errors.Is(err, errOutput) {
			return nil, errOutput
		}
		return nil, errHelper
	}
	return stdout.Bytes(), nil
}
