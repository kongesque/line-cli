package session

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxSessionBytes {
		return 0, errors.New("credential helper output exceeds limit")
	}
	return b.Buffer.Write(p)
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
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		r.code, r.err = exit.ExitCode(), nil
	}
	return r
}
