package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

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
	if err := a.Run([]string{"send", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--allow-plaintext", "--stdin", "--json"}); err != nil {
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

func TestMentionFlagsValidateBeforeStorage(t *testing.T) {
	for _, args := range [][]string{
		{"send", "id:c-group", "--text", "😀 @Peer", "--mentions", `[{"start":1,"end":2,"all":true}]`},
		{"send", "id:c-group", "--text", "hello", "--mentions", `[{"start":0,"end":5,"mid":"u-short"}]`},
		{"send", "id:c-group", "--text", "hello", "--mentions", `[{"start":0,"end":99,"all":true}]`},
		{"send", "id:c-group", "--text", "hello", "--mentions", `null`},
		{"send", "id:c-group", "--text", "hello", "--mentions", ``},
		{"send", "id:c-group", "--file", "nonexistent", "--mentions", `[]`},
		{"send", "id:c-group", "--mentions", `[]`},
		{"send", "id:u-peer", "--text", "@All", "--mentions", `[{"start":0,"end":4,"all":true}]`},
		{"send", "id:c-group", "--stdin", "--mentions", `[{"start":1,"end":2,"all":true}]`},
	} {
		a, _, _ := testApp(nil)
		a.Interactive = true
		a.In = strings.NewReader("😀 @All")
		a.Lock = func() (func(), error) { t.Fatal("invalid mentions opened storage"); return nil, nil }
		if err := a.Run(args); err == nil {
			t.Fatal("invalid mentions accepted")
		}
	}
}

func TestMentionTextAndStdinReplies(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		a, _, _ := testApp(nil)
		f := &messageAPI{history: []*line.Message{{ID: "9007199254740993", From: "u-self", To: "c-group", ToType: 2}}}
		a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		a.Manager.NewClient = func(string) session.API { return f }
		args := []string{"send", "id:c-group", "--allow-plaintext", "--reply-to", "9007199254740993", "--json", "--mentions", `[{"start":3,"end":8,"mid":"Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"start":9,"end":13,"all":true}]`}
		if stdin {
			args = append(args, "--stdin")
			a.In = strings.NewReader("😀 @Peer @All")
		} else {
			args = append(args, "--allow-plaintext", "--text", "😀 @Peer @All")
		}
		if err := a.Run(args); err != nil {
			t.Fatal(err)
		}
		if f.sent.Text != "😀 @Peer @All" || f.sent.RelatedMessageID != "9007199254740993" || !strings.Contains(f.sent.ContentMetadata["MENTION"], `"A":"1"`) {
			t.Fatal("mention/reply metadata lost")
		}
	}
}

func TestLiteralMentionDoesNotNotify(t *testing.T) {
	a, _, _ := testApp(nil)
	f := &messageAPI{}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"send", "id:c-group", "--allow-plaintext", "--text", "@All @Peer"}); err != nil {
		t.Fatal(err)
	}
	if f.sent.ContentMetadata["MENTION"] != "" {
		t.Fatal("literal text became a mention")
	}
}
