package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/internal/update"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestParseGlobalOptions(t *testing.T) {
	for _, tt := range []struct {
		args        []string
		format      string
		readOnly    bool
		rest        []string
		err         bool
		formatValid bool
	}{
		{nil, "text", false, nil, false, true},
		{[]string{"chats"}, "text", false, []string{"chats"}, false, true},
		{[]string{"--read-only", "send", "x"}, "text", true, []string{"send", "x"}, false, true},
		{[]string{"--error-format=json", "--read-only", "send"}, "json", true, []string{"send"}, false, true},
		{[]string{"--read-only", "--error-format", "json", "send"}, "json", true, []string{"send"}, false, true},
		{[]string{"--read-only=true", "send"}, "text", true, []string{"send"}, false, true},
		{[]string{"--read-only=1", "send"}, "text", true, []string{"send"}, false, true},
		{[]string{"--read-only=TRUE", "send"}, "text", true, []string{"send"}, false, true},
		{[]string{"--read-only=false", "send"}, "text", false, []string{"send"}, false, true},
		{[]string{"--read-only=0", "send"}, "text", false, []string{"send"}, false, true},
		// Options after the command belong to the command, never the global parser.
		{[]string{"send", "--read-only"}, "text", false, []string{"send", "--read-only"}, false, true},
		{[]string{"send", "--text", "--read-only"}, "text", false, []string{"send", "--text", "--read-only"}, false, true},
		{[]string{"--read-only=yes", "send"}, "text", false, []string{"send"}, true, true},
		{[]string{"--read-only=", "send"}, "text", false, []string{"send"}, true, true},
		{[]string{"--read-only", "--read-only", "send"}, "text", true, []string{"send"}, true, true},
		{[]string{"--error-format=json", "--error-format=text", "send"}, "json", false, []string{"send"}, true, true},
		{[]string{"--error-format=xml", "send"}, "text", false, []string{"send"}, true, false},
		{[]string{"--error-format"}, "text", false, nil, true, false},
		{[]string{"--read-only=yes", "--error-format=json", "send"}, "json", false, []string{"send"}, true, true},
	} {
		options, rest, formatValid, err := ParseGlobalOptions(tt.args)
		var typed *fault.Error
		if (err != nil) != tt.err || (err != nil && (!errors.As(err, &typed) || typed.Code != "invalid_arguments")) {
			t.Fatalf("%q: error %v", tt.args, err)
		}
		if options.ErrorFormat != tt.format || options.ReadOnly != tt.readOnly || !slices.Equal(rest, tt.rest) || formatValid != tt.formatValid {
			t.Fatalf("%q: got %+v %q %v", tt.args, options, rest, formatValid)
		}
	}
}

func TestParseReadOnlyEnv(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "false": false, "FALSE": false, "1": true, "true": true, "True": true} {
		if got, err := ParseReadOnlyEnv(value); err != nil || got != want {
			t.Fatalf("%q: %v %v", value, got, err)
		}
	}
	for _, value := range []string{"yes", "on", "2", " 1", "true "} {
		var typed *fault.Error
		if _, err := ParseReadOnlyEnv(value); !errors.As(err, &typed) || typed.Code != "invalid_arguments" {
			t.Fatalf("%q accepted: %v", value, err)
		}
	}
}

// sealedStore fails the test on any credential access.
type sealedStore struct{ t *testing.T }

func (s sealedStore) Load() (*session.State, error) {
	s.t.Fatal("read-only refusal loaded credentials")
	return nil, nil
}
func (s sealedStore) Save(*session.State) error { s.t.Fatal("read-only refusal saved"); return nil }
func (s sealedStore) Delete() error             { s.t.Fatal("read-only refusal deleted the session"); return nil }

type sealedInput struct{ t *testing.T }

func (r sealedInput) Read([]byte) (int, error) {
	r.t.Fatal("read-only refusal read input")
	return 0, nil
}

// sealedApp has no usable side effect: every lock, prompt, credential, network,
// updater, or migration hook fails the test.
func sealedApp(t *testing.T, interactive bool) (*App, *bytes.Buffer) {
	t.Helper()
	out := new(bytes.Buffer)
	m := session.NewManager(sealedStore{t})
	m.NewClient = func(string) session.API { t.Fatal("read-only refusal created a LINE client"); return nil }
	fatal := func(name string) func() (func(), error) {
		return func() (func(), error) { t.Fatal("read-only refusal acquired " + name); return nil, nil }
	}
	return &App{
		Out: out, Err: new(bytes.Buffer), In: sealedInput{t}, Interactive: interactive, Manager: m, Version: "test",
		Lock: fatal("the session lock"), WatchLock: fatal("the watch lock"),
		Updater:  &sealedUpdater{t},
		Password: func() (string, error) { t.Fatal("read-only refusal asked for a password"); return "", nil },
		Continue: func() error { t.Fatal("read-only refusal waited for the phone"); return nil },
		LoginQR: func(context.Context, func(session.QRLoginEvent) error) (*line.Profile, error) {
			t.Fatal("read-only refusal started QR login")
			return nil, nil
		},
		MigrateHeadless:  func(bool) error { t.Fatal("read-only refusal migrated storage"); return nil },
		NewHeadlessLogin: func() (session.LoginStorage, error) { t.Fatal("read-only refusal enrolled storage"); return nil, nil },
	}, out
}

type sealedUpdater struct{ t *testing.T }

func (u *sealedUpdater) Check(context.Context) (update.Plan, error) {
	u.t.Fatal("read-only refusal checked for updates")
	return update.Plan{}, nil
}
func (u *sealedUpdater) Install(context.Context, update.Plan) error {
	u.t.Fatal("read-only refusal installed an update")
	return nil
}

// mutatingInvocations covers every command that changes LINE or local
// credentials, explicitly and through guided prompts.
func mutatingInvocations() []struct {
	args   []string
	guided bool
	json   bool // supports --error-format=json
} {
	chat := "u" + strings.Repeat("A", 43)
	missing := filepath.Join("does-not-exist", "attachment.bin")
	return []struct {
		args   []string
		guided bool
		json   bool
	}{
		{[]string{"send", chat, "--text", "hello"}, false, true},
		{[]string{"send", chat, "--stdin", "--json"}, false, true},
		{[]string{"send", chat, "--file", missing}, false, true},
		{[]string{"send", chat, "--image", missing}, false, true},
		{[]string{"send", chat, "--text", "hi", "--reply-to", "123", "--require-e2ee"}, false, true},
		{[]string{"send", "Alice", "--text", "hello"}, false, true},
		{[]string{"react", chat, "--message", "123", "--reaction", "like"}, false, true},
		{[]string{"react", chat, "--message", "123", "--remove", "--json"}, false, true},
		{[]string{"unsend", chat, "--message", "123"}, false, true},
		{[]string{"read", chat}, false, true},
		{[]string{"read", chat, "--message", "123", "--json"}, false, true},
		{[]string{"read", "Alice"}, false, true},
		{[]string{"login"}, false, false},
		{[]string{"login", "--email", "you@example.com"}, false, false},
		{[]string{"login", "--qr-url", "--force"}, false, false},
		{[]string{"logout"}, false, false},
		{[]string{"auth", "migrate", "--storage=headless"}, false, false},
		{[]string{"update"}, false, false},
		{[]string{"update", "--json"}, false, false},
		{[]string{"send"}, true, false},
		{[]string{"send", chat}, true, false},
		{[]string{"react"}, true, false},
		{[]string{"unsend"}, true, false},
		{[]string{"read"}, true, false},
		{[]string{"login"}, true, false},
		{[]string{"login", "--headless"}, true, false},
		{[]string{"auth", "migrate", "--storage=headless"}, true, false},
	}
}

func TestReadOnlyRefusesEveryMutationBeforeSideEffects(t *testing.T) {
	for _, tt := range mutatingInvocations() {
		for _, mode := range []string{"flag", "env", "flag-after-format"} {
			a, out := sealedApp(t, tt.guided)
			args := append([]string{"--read-only"}, tt.args...)
			switch mode {
			case "env":
				args = tt.args
				a.Getenv = func(name string) string {
					if name != ReadOnlyEnv {
						t.Fatalf("read unexpected environment variable %q", name)
					}
					return "1"
				}
			case "flag-after-format":
				args = append([]string{"--error-format=text", "--read-only"}, tt.args...)
			}
			err := a.Run(args)
			var typed *fault.Error
			if !errors.Is(err, session.ErrReadOnly) || !errors.As(err, &typed) || typed.Code != "read_only" || typed.Stage != "preparation" {
				t.Fatalf("%s %q: got %v", mode, tt.args, err)
			}
			if session.StorageExitCode(err) != 1 || out.Len() != 0 {
				t.Fatalf("%s %q: exit %d, stdout %q", mode, tt.args, session.StorageExitCode(err), out.String())
			}
			if a.Manager.ReadOnly {
				t.Fatal("manager guard leaked past the command")
			}
		}
	}
}

func TestReadOnlyMachineErrorsReportNoAttempt(t *testing.T) {
	for _, tt := range mutatingInvocations() {
		if !tt.json {
			continue
		}
		a, out := sealedApp(t, false)
		err := a.Run(append([]string{"--read-only", "--error-format=json"}, tt.args...))
		var machine *MachineError
		if !errors.As(err, &machine) || machine.Detail.Code != "read_only" || machine.Detail.Stage != "preparation" || out.Len() != 0 {
			t.Fatalf("%q: %+v", tt.args, err)
		}
		op := machine.Detail.Mutation
		if op == nil || op.Action != tt.args[0] || op.Attempted || op.Outcome != fault.NotAttempted || op.RequestSequence != nil || len(op.Steps) != 0 {
			t.Fatalf("%q: mutation %+v", tt.args, op)
		}
	}
}

func TestInvalidReadOnlyEnvironmentFailsClosed(t *testing.T) {
	for _, args := range [][]string{{"send", "u" + strings.Repeat("A", 43), "--text", "hi"}, {"chats"}, {"help"}} {
		a, _ := sealedApp(t, false)
		a.Getenv = func(string) string { return "yes" }
		var typed *fault.Error
		if err := a.Run(args); !errors.As(err, &typed) || typed.Code != "invalid_arguments" {
			t.Fatalf("%q: %v", args, err)
		}
		a.Getenv = func(string) string { return "on" }
		var machine *MachineError
		if err := a.Run(append([]string{"--error-format=json"}, args...)); !errors.As(err, &machine) || machine.Detail.Code != "invalid_arguments" {
			t.Fatalf("%q JSON: %v", args, err)
		}
	}
	// --read-only=false never overrides the environment switch.
	a, _ := sealedApp(t, false)
	a.Getenv = func(string) string { return "true" }
	if err := a.Run([]string{"--read-only=false", "logout"}); !errors.Is(err, session.ErrReadOnly) {
		t.Fatalf("flag disabled the environment switch: %v", err)
	}
}

func TestReadOnlyAllowsHelpAndLocalInformation(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"version"}, {"--help"}, {}, {"send", "--help"}, {"read", "--help"}, {"react", "-h"}, {"login", "--help"}, {"update", "--help"}, {"logout", "--help"}, {"auth", "migrate", "--help"}} {
		a, _ := sealedApp(t, false)
		if err := a.Run(append([]string{"--read-only"}, args...)); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
}

func TestReadOnlyAllowsReadsAndUpdateCheck(t *testing.T) {
	a, out, _ := testApp(&readAPI{})
	a.Getenv = func(string) string { return "1" }
	if err := a.Run([]string{"whoami", "--json"}); err != nil || !strings.Contains(out.String(), `"mid": "u1"`) {
		t.Fatalf("whoami: %v %q", err, out.String())
	}
	f := &chatAPI{boxes: []line.MessageBox{{ID: "u-one", UnreadCount: "2"}}, names: map[string]string{"u-one": "Alice"}}
	a = chatTestApp(f)
	if err := a.Run([]string{"--read-only", "chats", "--search", "Ali"}); err != nil || !strings.Contains(a.Out.(*bytes.Buffer).String(), "Alice") {
		t.Fatalf("chats: %v", err)
	}
	g, _, out2, _, _ := guidedApp(t, "")
	g.Interactive = false
	if err := g.Run([]string{"--read-only", "messages", "id:u-alice", "--json"}); err != nil || !strings.Contains(out2.String(), "Their message") {
		t.Fatalf("messages: %v", err)
	}
	u, updater, out3, _ := updateApp()
	if err := u.Run([]string{"--read-only", "update", "--check"}); err != nil || updater.checks != 1 || updater.installs != 0 || !strings.Contains(out3.String(), "No changes made") {
		t.Fatalf("update --check: %v", err)
	}
}

func TestReadOnlyGuardsSessionDuringCommand(t *testing.T) {
	a, _, _ := testApp(nil)
	sawGuard := false
	a.Manager.NewClient = func(string) session.API {
		sawGuard = a.Manager.ReadOnly
		return &readAPI{}
	}
	if err := a.Run([]string{"--read-only", "whoami"}); err != nil || !sawGuard || a.Manager.ReadOnly {
		t.Fatalf("manager guard not applied for the command only: %v %v %v", err, sawGuard, a.Manager.ReadOnly)
	}
}
