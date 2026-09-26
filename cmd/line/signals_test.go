package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/cli"
	"github.com/kongesque/line-cli/internal/session"
)

type confirmationStore struct{}

func (confirmationStore) Load() (*session.State, error) {
	return &session.State{MID: "synthetic", Generation: "old", AccessToken: "synthetic"}, nil
}
func (confirmationStore) Save(*session.State) error { panic("unexpected save") }
func (confirmationStore) Delete() error             { panic("unexpected delete") }

// Invoked in a subprocess with synthetic storage; never contacts LINE/keyrings.
func TestSignalInputHelper(t *testing.T) {
	mode := os.Getenv("LINE_CLI_TEST_INPUT_MODE")
	if mode == "" {
		return
	}
	s := newCommandSignals()
	fd := int(os.Stdin.Fd())
	source, closeInput := commandInput(s.ctx, fd)
	in := bufio.NewReader(source)
	var err error
	switch mode {
	case "password":
		_, err = readPassword(s.ctx, fd, in, os.Stderr)
	case "continue":
		err = waitForPhone(s.ctx, in, os.Stderr)
	case "confirmation":
		a := &cli.App{Interactive: true, Context: s.ctx, In: in, Out: os.Stdout, Err: os.Stderr,
			Manager: &session.Manager{Store: confirmationStore{}}, Lock: func() (func(), error) { return func() {}, nil }}
		err = a.Run([]string{"login"})
	case "poll":
		io.WriteString(os.Stderr, "Waiting for scan\n")
		<-s.ctx.Done()
		err = s.ctx.Err()
	default:
		panic("unknown helper mode")
	}
	code := commandExit(err, int(s.code.Load()), os.Stderr)
	closeInput()
	s.close()
	os.Exit(code)
}

func TestSignalExitWithRedirectedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support sending Unix signals to subprocesses")
	}
	for _, mode := range []string{"confirmation", "continue", "poll"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(mode+"/"+sig.String(), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSignalInputHelper$")
				cmd.Env = append(os.Environ(), "LINE_CLI_TEST_INPUT_MODE="+mode)
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
				stderr, err := cmd.StderrPipe()
				if err != nil {
					t.Fatal(err)
				}
				var stdout bytes.Buffer
				cmd.Stdout = &stdout
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				marker := map[string]string{"confirmation": "Continue? [y/N]: ", "continue": "press Enter to continue: ", "poll": "Waiting for scan\n"}[mode]
				var before strings.Builder
				var b [1]byte
				for !strings.Contains(before.String(), marker) {
					if _, err := io.ReadFull(stderr, b[:]); err != nil {
						t.Fatal("helper did not reach input", err)
					}
					before.WriteByte(b[0])
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				after, _ := io.ReadAll(stderr)
				err = cmd.Wait()
				var exit *exec.ExitError
				want := 130
				if sig == syscall.SIGTERM {
					want = 143
				}
				if !errors.As(err, &exit) || exit.ExitCode() != want {
					t.Fatalf("exit = %v, want %d", err, want)
				}
				if stdout.Len() != 0 || !strings.Contains(string(after), "Cancelled.") {
					t.Fatal("wrong cancellation output")
				}
			})
		}
	}
}

func TestCommandExitRetainsLoginOutcome(t *testing.T) {
	for _, outcome := range []*session.LoginError{
		{Stage: session.LoginFinal, Dispatched: true},
		{Stage: session.LoginSetup, Dispatched: true, Approved: true},
		{Stage: session.LoginSave, Dispatched: true, Approved: true, SaveUncertain: true},
	} {
		var out bytes.Buffer
		if commandExit(outcome, 143, &out) != 143 || !strings.Contains(out.String(), outcome.Error()) || !strings.Contains(out.String(), "may already have replaced") {
			t.Fatal("signal hid login outcome")
		}
	}
	if commandExit(nil, 0, io.Discard) != 0 || commandExit(cli.ErrCancelled, 0, io.Discard) != 0 {
		t.Fatal("refusal should succeed")
	}
}

func TestPasswordLine(t *testing.T) {
	for _, text := range []string{"synthetic\n", "synthetic\r\n", "syntheticx\b\n"} {
		got, err := readPasswordLine(context.Background(), strings.NewReader(text))
		if err != nil || got != "synthetic" {
			t.Fatal("password input changed", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPasswordLine(ctx, strings.NewReader("secret\n")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
