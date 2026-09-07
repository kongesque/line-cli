package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/highesttt/matrix-line-messenger/internal/messaging"
	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

type messageAPI struct {
	session.API
	history []*line.Message
	sent    *line.Message
}

func (f *messageAPI) GetRecentMessagesV2(string, int) ([]*line.Message, error) { return f.history, nil }
func (f *messageAPI) GetBlockedContactIds() ([]string, error)                  { return nil, nil }
func (f *messageAPI) SendMessage(_ int64, msg *line.Message) (*line.Message, error) {
	f.sent = msg
	return &line.Message{ID: "message-id"}, nil
}

func TestMessageArgumentsValidateBeforeSessionAccess(t *testing.T) {
	for _, args := range [][]string{
		{"messages", "--help"}, {"send", "--help"}, {"messages"},
		{"messages", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--limit", "0"}, {"messages", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--limit", "101"},
		{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--text", ""},
		{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--text", "hello", "--stdin"},
		{"send", "bad\nname", "--text", "hello"},
		{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--text", "hello", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, _, _ := testApp(nil)
			a.Lock = func() (func(), error) { t.Fatal("session opened for invalid arguments"); return nil, nil }
			_ = a.Run(args)
		})
	}
}

func TestSendStdinPreservesMultilineTextAndJSONOutput(t *testing.T) {
	a, out, diagnostics := testApp(nil)
	f := &messageAPI{}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "token", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	a.In = strings.NewReader("hello\nworld\n")
	if err := a.Run([]string{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--stdin", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result messaging.SendResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != "message-id" || result.Encrypted || f.sent.Text != "hello\nworld\n" || diagnostics.Len() != 0 {
		t.Fatal("bad stdin send")
	}
}

func TestHistoryPartialFailureStillReturnsStructuredJSON(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &messageAPI{history: []*line.Message{{ID: "plain", Text: "hello"}, {ID: "encrypted", ContentMetadata: map[string]string{"e2eeVersion": "2"}}}}
	a.Manager.NewClient = func(string) session.API { return f }
	err := a.Run([]string{"messages", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--json"})
	if err == nil {
		t.Fatal("missing partial failure status")
	}
	var result []messaging.Message
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Text != "hello" || result[1].Status != "decryption_failed" || result[1].Text != "" {
		t.Fatal("invalid partial history output")
	}
}

func TestSendStdinHasBoundedSize(t *testing.T) {
	a, _, _ := testApp(nil)
	a.In = strings.NewReader(strings.Repeat("x", messaging.MaxTextUnits*4+1))
	a.Lock = func() (func(), error) { t.Fatal("session opened before input validation"); return nil, nil }
	if err := a.Run([]string{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--stdin"}); err == nil {
		t.Fatal("accepted oversized stdin")
	}
}
