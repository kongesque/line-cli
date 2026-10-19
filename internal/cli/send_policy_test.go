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

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestCLIRequiresE2EEWhenRequested(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		for _, input := range []string{"text", "stdin", "file", "image"} {
			t.Run(input+map[bool]string{false: "/human", true: "/json"}[jsonMode], func(t *testing.T) {
				a, out, _ := testApp(nil)
				api := &imageSendAPI{}
				store := &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
				a.Manager.Store = store
				a.Manager.NewClient = func(string) session.API { return api }
				a.In = strings.NewReader("synthetic")
				args := []string{"--error-format=json", "send", "id:u-peer", "--require-e2ee"}
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
					t.Fatal("strict E2EE send changed state")
				}
				encoded, _ := json.Marshal(machineDetail(t, err))
				if !strings.Contains(string(encoded), `"code":"encryption_required"`) || !strings.Contains(strings.ToLower(err.Error()), "nothing was sent") {
					t.Fatal("missing safe encryption policy error")
				}
			})
		}
	}
}

func TestNonE2EEFlagAndLegacyAlias(t *testing.T) {
	for _, flagName := range []string{"--allow-non-e2ee", "--allow-plaintext"} {
		for _, input := range []string{"text", "stdin", "file", "image"} {
			for _, value := range []string{"true", "false"} {
				t.Run(flagName+"/"+input+"/"+value, func(t *testing.T) {
					a, api, store, out, diagnostics, _ := consentFixture(t, "unused")
					store.state.NoE2EE = true
					args := []string{"--error-format=json", "send", "id:u-peer", flagName + "=" + value, "--json"}
					switch input {
					case "text":
						args = append(args, "--text", "synthetic")
					case "stdin":
						a.In = strings.NewReader("synthetic")
						args = append(args, "--stdin")
					case "file":
						args = append(args, "--file", consentFile(t))
					case "image":
						args = append(args, "--image", cliImageFixture(t))
					}
					err := a.Run(args)
					if diagnostics.Len() != 0 {
						t.Fatal("explicit flag displayed an interactive prompt")
					}
					if value == "false" {
						if err == nil || api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 {
							t.Fatal("false flag permitted a send or changed state")
						}
						if machineDetail(t, err).Code != "encryption_required" || !strings.Contains(err.Error(), "default send policy") || !strings.Contains(err.Error(), "HTTPS") {
							t.Fatal("missing strict-policy refusal or default-policy guidance")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					wantUploads := 0
					if input == "file" || input == "image" {
						wantUploads = 1
					}
					var result messaging.SendResult
					if json.Unmarshal(out.Bytes(), &result) != nil || result.Encrypted || result.ID != "message-id" ||
						api.sends != 1 || api.uploads != wantUploads || result.RequestSequence == 0 || result.RequestSequence != store.state.LastReqSeq {
						t.Fatal("flag did not preserve a single send and its non-E2EE receipt")
					}
				})
			}
		}
	}
}

func TestNonE2EEFlagsDoNotBypassMissingKeys(t *testing.T) {
	for _, flagName := range []string{"--allow-non-e2ee", "--allow-plaintext"} {
		t.Run(flagName, func(t *testing.T) {
			a, api, store, out, diagnostics, _ := consentFixture(t, "unused")
			store.state.NoE2EE = false
			store.state.ExportedKeys = nil
			err := a.Run([]string{"send", "id:u-peer", flagName, "--text", "synthetic", "--json"})
			if err == nil || api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 || diagnostics.Len() != 0 {
				t.Fatal("flag bypassed missing keys or prompted for a downgrade")
			}
		})
	}
}

func TestSendHelpDescribesNonE2EETransport(t *testing.T) {
	a, _, diagnostics := testApp(nil)
	a.Lock = func() (func(), error) { t.Fatal("help accessed session"); return nil, nil }
	if err := a.Run([]string{"send", "--help"}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"-require-e2ee", "-allow-non-e2ee", "HTTPS/TLS", "end-to-end encryption", "LINE can read", "-allow-plaintext", "compatibility alias"} {
		if !strings.Contains(diagnostics.String(), text) {
			t.Fatalf("help is missing %q", text)
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

func TestInteractiveFileSendsWithoutConfirmation(t *testing.T) {
	a, api, store, out, diagnostics, locked := consentFixture(t, "")
	a.In = checkedInput{t: t, locked: locked, Reader: strings.NewReader(""), onRead: func() {
		t.Fatal("explicit send asked for encryption confirmation")
	}}
	if err := a.Run([]string{"send", "id:u-peer", "--file", consentFile(t)}); err != nil {
		t.Fatal(err)
	}
	if api.sends != 1 || api.uploads != 1 || store.state.LastReqSeq == 0 || string(api.data) != "synthetic file\n" || !strings.Contains(out.String(), "transport encrypted (no E2EE)") || diagnostics.Len() != 0 || *locked {
		t.Fatal("default send failed, prompted, or retained lock")
	}
}

func TestGuidedMessageWithoutEncryptionConfirmation(t *testing.T) {
	a, api, _, diagnostics, _ := guidedApp(t, "/New\n1\nsynthetic\n")
	if err := a.Run([]string{"send"}); err != nil || api.sends != 1 || api.sent.Text != "synthetic" || strings.Contains(diagnostics.String(), "Send this message?") {
		t.Fatal("guided send failed", err)
	}
}

func TestSendScriptsUseAutomaticNonE2EEWithoutPrompt(t *testing.T) {
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
			if err != nil || api.sends != 1 || store.state.LastReqSeq == 0 {
				t.Fatal("default script send failed", err)
			}
			if diagnostics.Len() != 0 {
				t.Fatal("script or flag displayed prompt")
			}
		})
	}
}

func TestSendStopsOnInvalidPreparation(t *testing.T) {
	for _, change := range []string{"account", "invalidated", "missing-keys", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			a, api, store, out, _, locked := consentFixture(t, "y\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a.Context = ctx
			switch change {
			case "account":
				a.promptAccount = &promptAccount{mid: store.state.MID, generation: "previous"}
			case "invalidated":
				store.state.Invalidated = true
			case "missing-keys":
				store.state.NoE2EE = false
				store.state.ExportedKeys = nil
			case "cancelled":
				cancel()
			}
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
				if api.sends != 1 || api.uploads != wantUploads || diagnostics.Len() != 0 {
					t.Fatal("mutation was replayed or re-prompted")
				}
			} else if api.sends != 0 || api.uploads != 0 || diagnostics.Len() != 0 || store.state.LastReqSeq != 0 {
				t.Fatal("error prompted for a downgrade")
			}
		})
	}
}

func TestStrictE2EEOverridesCompatibilityFlags(t *testing.T) {
	for _, flags := range [][]string{
		{"--require-e2ee", "--allow-non-e2ee"},
		{"--allow-non-e2ee", "--require-e2ee"},
		{"--require-e2ee", "--allow-plaintext"},
		{"--allow-plaintext", "--require-e2ee"},
	} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			a, api, store, out, diagnostics, _ := consentFixture(t, "")
			args := append([]string{"send", "id:u-peer", "--text", "synthetic", "--json"}, flags...)
			err := a.Run(args)
			if err == nil || machineDetail(t, err).Code != "encryption_required" || api.sends != 0 || api.uploads != 0 || store.state.LastReqSeq != 0 || out.Len() != 0 || diagnostics.Len() != 0 {
				t.Fatal("compatibility flag bypassed strict encryption or prompted", err)
			}
		})
	}
}
