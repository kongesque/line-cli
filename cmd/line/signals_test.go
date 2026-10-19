package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
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
	case "output":
		store := &signalSendStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		manager := session.NewManager(store)
		manager.NewClient = func(string) session.API { return &cancelledSendAPI{cancel: func() {}, confirmed: true} }
		a := &cli.App{Context: s.ctx, Out: os.Stdout, Err: os.Stderr, Manager: manager, Lock: func() (func(), error) { return func() {}, nil }}
		err = a.Run([]string{"--error-format=json", "send", "id:c-group", "--text", "synthetic", "--json"})
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

type cancelledSendAPI struct {
	session.API
	cancel    context.CancelFunc
	confirmed bool
}

func (f *cancelledSendAPI) SendMessageContext(context.Context, int64, *line.Message) (*line.Message, error) {
	f.cancel()
	if f.confirmed {
		return &line.Message{ID: "123"}, nil
	}
	return nil, context.Canceled
}
func TestCommandExitPreservesCancelledMutationDetails(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		store := &signalSendStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		manager := session.NewManager(store)
		manager.NewClient = func(string) session.API { return &cancelledSendAPI{cancel: cancel, confirmed: confirmed} }
		client, err := messaging.New(manager)
		if err != nil {
			t.Fatal(err)
		}
		var want string
		if confirmed {
			_, err = client.SendFileWithOptionsContext(ctx, "c-group", messaging.Attachment{Name: "test.txt"}, "", messaging.SendOptions{AllowPlaintext: true})
			want = "message 123 was created"
		} else {
			_, err = client.SendMentionsWithOptionsContext(ctx, "c-group", "synthetic", "", nil, messaging.SendOptions{AllowPlaintext: true})
			want = "delivery may have occurred"
		}
		var out bytes.Buffer
		if code := commandExit(err, 143, &out); code != 143 || !strings.Contains(out.String(), want) {
			t.Fatalf("code=%d output=%s", code, out.String())
		}
	}
}

type signalSendStore struct{ state *session.State }

func (s *signalSendStore) Load() (*session.State, error)   { return s.state, nil }
func (s *signalSendStore) Save(state *session.State) error { s.state = state; return nil }
func (s *signalSendStore) Delete() error                   { return nil }

func TestMachineErrorExitChannelAndStorageCodes(t *testing.T) {
	for _, tc := range []struct {
		err          error
		signal, code int
	}{
		{session.ErrBusy, 0, 75}, {session.ErrStorageFormat, 0, 65},
		{session.ErrDurabilityUncertain, 0, 74}, {context.Canceled, 0, 130},
		{context.DeadlineExceeded, 0, 1}, {context.Canceled, 143, 143},
	} {
		var stdout, stderr bytes.Buffer
		a := &cli.App{Out: &stdout, Err: &stderr, Lock: func() (func(), error) { return nil, tc.err }}
		err := a.Run([]string{"--error-format=json", "whoami", "--json"})
		code := commandExit(err, tc.signal, &stderr)
		var envelope struct {
			Schema int `json:"schema"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
			ExitCode int `json:"exit_code"`
		}
		if json.Unmarshal(stderr.Bytes(), &envelope) != nil || code != tc.code || envelope.ExitCode != tc.code || envelope.Schema != 1 || envelope.Error.Code == "" || stdout.Len() != 0 {
			t.Fatalf("bad envelope/code: %d %s", code, stderr.String())
		}
	}
}

func TestBrokenStdoutPipeRetainsConfirmedReceipt(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	readEnd.Close()
	defer writeEnd.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSignalInputHelper$")
	cmd.Env = append(os.Environ(), "LINE_CLI_TEST_INPUT_MODE=output")
	cmd.Stdout = writeEnd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	var envelope struct {
		Error struct {
			Code     string `json:"code"`
			Mutation struct {
				Outcome   string `json:"outcome"`
				MessageID string `json:"message_id"`
			} `json:"mutation"`
		} `json:"error"`
	}
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || json.Unmarshal(stderr.Bytes(), &envelope) != nil || envelope.Error.Code != "output_failed" || envelope.Error.Mutation.Outcome != "confirmed" || envelope.Error.Mutation.MessageID != "123" {
		t.Fatalf("lost receipt after broken pipe: %v %s", err, stderr.String())
	}
}
