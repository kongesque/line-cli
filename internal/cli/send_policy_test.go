package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestCLIRequiresPlaintextConsent(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, input := range []string{"text", "stdin", "file", "image"} {
			t.Run(input+map[bool]string{false: "/human", true: "/json"}[jsonMode], func(t *testing.T) {
				a, out, _ := testApp(nil)
				api := &imageSendAPI{}
				store := &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
				a.Manager.Store = store
				a.Manager.NewClient = func(string) session.API { return api }
				a.In = strings.NewReader("synthetic")
				args := []string{"--error-format=json", "send", "id:u-peer"}
				switch input {
				case "text":
					args = append(args, "--text", "synthetic")
				case "stdin":
					args = append(args, "--stdin")
				default:
					args = append(args, "--"+input, cliImageFixture(t))
				}
				if jsonMode {
					args = append(args, "--json")
				}
				err := a.Run(args)
				if err == nil || api.sent != nil || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 {
					t.Fatal("send without consent changed state")
				}
				encoded, _ := json.Marshal(machineDetail(t, err))
				if !strings.Contains(string(encoded), `"code":"encryption_required"`) || !strings.Contains(strings.ToLower(err.Error()), "nothing was sent") {
					t.Fatal("missing safe consent error")
				}
			})
		}
	}
}

// All fields and attachment data in these tests are synthetic.
type consentAPI struct {
	imageSendAPI
	sends   int
	sendErr error
}

func (f *consentAPI) SendMessageContext(_ context.Context, _ int64, m *line.Message) (*line.Message, error) {
	f.sends++
	f.sent = m
	return &line.Message{ID: "message-id"}, f.sendErr
}

func consentFixture(t *testing.T, answer string) (*App, *consentAPI, *testStore, *bytes.Buffer, *bytes.Buffer, *bool) {
	t.Helper()
	a, _, out, diagnostics, locked := guidedApp(t, answer)
	api := &consentAPI{}
	a.Manager.NewClient = func(string) session.API { return api }
	store := a.Manager.Store.(*testStore)
	return a, api, store, out, diagnostics, locked
}

func consentFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte("synthetic file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInteractiveFileConsentBeforeMutations(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		accepted     bool
	}{
		{"yes", "y\n", true}, {"uppercase", "YES\n", true}, {"no", "n\n", false},
		{"default", "\n", false}, {"invalid", "maybe\n", false}, {"eof", "", false}, {"unfinished", "y", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := tc.answer
			a, api, store, out, diagnostics, locked := consentFixture(t, answer)
			a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader(answer), onRead: func() {
				if api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 {
					t.Fatal("consent did not precede mutations")
				}
			}}
			err := a.Run([]string{"send", "id:u-peer", "--file", consentFile(t)})
			if !tc.accepted {
				if !errors.Is(err, ErrCancelled) || api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 {
					t.Fatal("declined/unfinished consent changed remote state", err)
				}
			} else if err != nil || api.sends != 1 || api.uploads != 1 || store.state.LastReqSeq == 0 || string(api.data) != "synthetic file\n" || !strings.Contains(out.String(), "not end-to-end encrypted") {
				t.Fatal("confirmed file did not send once", err)
			}
			if !strings.Contains(diagnostics.String(), "Send this file? [y/N]") || strings.Count(diagnostics.String(), "Send this file?") != 1 || *locked {
				t.Fatal("incorrect prompt or lock retained")
			}
		})
	}
}

func TestGuidedMessageConsent(t *testing.T) {
	a, api, _, diagnostics, _ := guidedApp(t, "/New\n1\nsynthetic\ny\n")
	if err := a.Run([]string{"send"}); err != nil || api.sends != 1 || api.sent.Text != "synthetic" || !strings.Contains(diagnostics.String(), "Send this message? [y/N]") {
		t.Fatal("guided consent failed", err)
	}
}

func TestSendScriptsNeverPromptForPlaintext(t *testing.T) {
	for _, mode := range []string{"noninteractive", "json", "json-errors", "stdin", "flag"} {
		t.Run(mode, func(t *testing.T) {
			a, api, store, _, diagnostics, _ := consentFixture(t, "unused")
			store.state.NoE2EE = true
			args := []string{"send", "id:u-peer", "--text", "synthetic"}
			switch mode {
			case "noninteractive":
				a.Interactive = false
			case "json":
				args = append(args, "--json")
			case "json-errors":
				args = append([]string{"--error-format=json"}, args...)
			case "stdin":
				args = []string{"send", "id:u-peer", "--stdin"}
				a.In = strings.NewReader("synthetic")
			case "flag":
				args = append(args, "--allow-plaintext")
			}
			err := a.Run(args)
			if mode == "flag" {
				if err != nil || api.sends != 1 {
					t.Fatal("explicit flag failed", err)
				}
			} else if err == nil || api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 {
				t.Fatal("script bypassed consent", err)
			}
			if diagnostics.Len() != 0 {
				t.Fatal("script or flag displayed prompt")
			}
		})
	}
}

func TestConsentRechecksAccountAndPreparation(t *testing.T) {
	for _, change := range []string{"account", "invalidated", "missing-keys", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			a, api, store, out, _, locked := consentFixture(t, "y\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.Context = ctx
			a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader("y\n"), onRead: func() {
				switch change {
				case "account":
					store.state.Generation = "replacement"
				case "invalidated":
					store.state.Invalidated = true
				case "missing-keys":
					store.state.NoE2EE = false
					store.state.ExportedKeys = nil
				case "cancelled":
					cancel()
				}
			}}
			if err := a.Run([]string{"send", "id:u-peer", "--file", consentFile(t)}); err == nil {
				t.Fatal("changed preparation allowed send")
			}
			if api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 || *locked {
				t.Fatal("changed preparation caused mutation or retained lock")
			}
		})
	}
}

func TestInteractiveSendDoesNotPromptOrReplayAfterFailures(t *testing.T) {
	for _, failure := range []string{"missing-keys", "send", "upload"} {
		t.Run(failure, func(t *testing.T) {
			a, api, store, _, diagnostics, _ := consentFixture(t, "y\n")
			switch failure {
			case "missing-keys":
				store.state.NoE2EE = false
				store.state.ExportedKeys = nil
			case "send":
				api.sendErr = errors.New("synthetic connection lost")
			case "upload":
				api.err = errors.New("synthetic upload failure")
			}
			if err := a.Run([]string{"send", "id:u-peer", "--file", consentFile(t)}); err == nil {
				t.Fatal("expected failure")
			}
			if failure == "send" || failure == "upload" {
				wantUploads := 0
				if failure == "upload" {
					wantUploads = 1
				}
				if api.sends != 1 || api.uploads != wantUploads || strings.Count(diagnostics.String(), "Send this file?") != 1 {
					t.Fatal("mutation was replayed or re-prompted")
				}
			} else if api.sends != 0 || api.uploads != 0 || diagnostics.Len() != 0 || store.state.LastReqSeq != 0 {
				t.Fatal("error prompted for a downgrade")
			}
		})
	}
}

func TestPlaintextConsentCannotResumeRemotePreparation(t *testing.T) {
	seq := int64(1)
	for _, tc := range []struct {
		name string
		op   fault.Mutation
	}{
		{"upload", fault.Mutation{Action: "send", Outcome: fault.NotAttempted, Steps: []fault.Step{{Stage: "attachment_upload", Attempted: true, Outcome: fault.Confirmed}}}},
		{"registration", fault.Mutation{Action: "send", Outcome: fault.NotAttempted, Steps: []fault.Step{{Stage: "group_key_registration", Attempted: true, Outcome: fault.Unknown}}}},
		{"sequence", fault.Mutation{Action: "send", Outcome: fault.NotAttempted, RequestSequence: &seq}},
		{"send", fault.Mutation{Action: "send", Attempted: true, Outcome: fault.Unknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := fault.New("encryption_required", "preparation", "synthetic refusal", &messaging.MutationError{Stage: "preparation", Mutation: tc.op})
			if canConfirmPlaintext(err) {
				t.Fatal("consent could replay remote work")
			}
		})
	}
}
