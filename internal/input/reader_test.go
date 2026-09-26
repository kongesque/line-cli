package input

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type blockedReader struct{ started, release, done chan struct{} }

func (b blockedReader) Read(p []byte) (int, error) {
	close(b.started)
	<-b.release
	n := copy(p, "secret")
	close(b.done)
	return n, nil
}

func TestCancelledReadCannotWriteCallerBuffer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := blockedReader{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	r := NewReader(ctx, b)
	p := []byte("unchanged")
	result := make(chan error, 1)
	go func() { _, err := r.Read(p); result <- err }()
	<-b.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not cancel")
	}
	close(b.release)
	<-b.done
	if string(p) != "unchanged" {
		t.Fatal("late read changed caller memory")
	}
	if _, err := r.Read(p); !errors.Is(err, context.Canceled) {
		t.Fatal("read reused after cancellation")
	}
}

func TestReaderPreservesDataAndEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data, err := io.ReadAll(NewReader(ctx, strings.NewReader("first\nsecond\n")))
	if err != nil || string(data) != "first\nsecond\n" {
		t.Fatal("lost input", err)
	}
}
