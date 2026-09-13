package session

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

var errHelperOutputLimit = errors.New("credential helper output exceeds limit")

type boundedOutput struct {
	// Embedding bytes.Buffer would promote ReadFrom and let os/exec's io.Copy
	// bypass Write, removing the output bound.
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }
func (b *boundedOutput) Len() int      { return b.buffer.Len() }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxSessionBytes-b.Len() {
		b.exceeded = true
		return 0, errHelperOutputLimit
	}
	return b.buffer.Write(p)
}
func runSecretTool(args []string, input []byte) secretResult {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "secret-tool", args...)
	var stdout, stderr boundedOutput
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := secretResult{output: stdout.Bytes(), diagnostic: stderr.Len() > 0, err: err}
	defer clear(stderr.Bytes())
	if stdout.exceeded || stderr.exceeded {
		// The helper may exit or receive SIGPIPE after the copy fails. Preserve
		// the transport error instead of interpreting its exit as a missing key.
		clear(r.output)
		r.output, r.err = nil, errHelperOutputLimit
		r.diagnostic = r.diagnostic || stderr.exceeded
		return r
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		r.code, r.err = exit.ExitCode(), nil
	}
	return r
}
