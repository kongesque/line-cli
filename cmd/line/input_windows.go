package main

import (
	"context"
	"io"
	"os"

	"github.com/kongesque/line-cli/internal/input"
)

func commandInput(ctx context.Context, _ int) (io.Reader, func()) {
	return input.NewReader(ctx, os.Stdin), func() {}
}
