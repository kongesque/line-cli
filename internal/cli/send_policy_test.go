package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
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
func TestGuidedSendAlsoRequiresPlaintextConsent(t *testing.T) {
	a, api, _, _, _ := guidedApp(t, "/New\n1\nsynthetic\n")
	err := a.Run([]string{"send"})
	if err == nil || !strings.Contains(err.Error(), "--allow-plaintext") || api.sends != 0 {
		t.Fatal("interactive send bypassed consent", err)
	}
}
