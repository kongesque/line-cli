package cli

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestEveryHelpGoesToStdoutWithExamples(t *testing.T) {
	singleDash := regexp.MustCompile(`(?m)^\s+-[a-z]{2,}`)
	for _, c := range completionKeys(completionSpec()) {
		words := strings.Fields(c.name)
		for _, flag := range []string{"--help", "-h"} {
			a, out, diagnostics := testApp(nil)
			a.Lock = func() (func(), error) { t.Fatal("help opened the session"); return nil, nil }
			if err := a.Run(append(append([]string{}, words...), flag)); err != nil {
				t.Fatalf("%s %s: %v", c.name, flag, err)
			}
			help := out.String()
			if diagnostics.Len() != 0 || !strings.HasPrefix(help, "Usage: line ") {
				t.Fatalf("%s %s: stdout %q stderr %q", c.name, flag, help, diagnostics.String())
			}
			if singleDash.MatchString(help) {
				t.Fatalf("%s help uses single-dash flags: %q", c.name, help)
			}
			if c.name != "help" && c.name != "version" && !strings.Contains(help, "\nExamples:\n  line ") {
				t.Fatalf("%s help has no examples", c.name)
			}
			// line help COMMAND prints the same page.
			b, viaHelp, _ := testApp(nil)
			if err := b.Run(append([]string{"help"}, words...)); err != nil || viaHelp.String() != help {
				t.Fatalf("line help %s differs: %v", c.name, err)
			}
		}
	}
}

func TestBareHelpForLogoutAndAuth(t *testing.T) {
	a, out, _ := testApp(nil)
	if err := a.Run([]string{"logout", "--help"}); err != nil || !strings.Contains(out.String(), "does not sign out\nyour phone") || !strings.Contains(out.String(), "account stays active") {
		t.Fatalf("logout help %q %v", out.String(), err)
	}
	out.Reset()
	if err := a.Run([]string{"auth"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--check", "--json", "migrate", "headless", "line doctor"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("auth help lacks %q: %q", want, out.String())
		}
	}
}

func TestSendHelpGroupsFlagsByPurpose(t *testing.T) {
	a, out, _ := testApp(nil)
	if err := a.Run([]string{"send", "--help"}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	order := []string{"\nMessage:\n", "  --text TEXT", "\nTarget:\n", "  --reply-to ID", "\nDelivery:\n", "  --require-e2ee", "\nOutput:\n", "  --json"}
	last := -1
	for _, want := range order {
		i := strings.Index(help, want)
		if i <= last {
			t.Fatalf("%q out of order in %q", want, help)
		}
		last = i
	}
}

func TestErrorsSuggestAndStayOneLine(t *testing.T) {
	chat := "u" + strings.Repeat("A", 43)
	for _, tc := range []struct {
		args []string
		want string
		not  string
	}{
		{[]string{"chats", "--limt", "5"}, "unknown option --limt; did you mean --limit? Run line chats --help", ""},
		{[]string{"send", chat, "--txt", "hi"}, "did you mean --text?", ""},
		{[]string{"chats", "--bogusflag"}, "unknown option --bogusflag; run line chats --help", "did you mean"},
		{[]string{"chat"}, `unknown command "chat"; did you mean line chats?`, ""},
		{[]string{"mesages"}, "did you mean line messages?", ""},
		{[]string{"sned"}, "did you mean line send?", ""},
		{[]string{"invite"}, "did you mean line invites?", ""},
		{[]string{"zzzzzz"}, `unknown command "zzzzzz"; run line help`, "did you mean"},
		{[]string{"--read-onyl", "chats"}, "did you mean --read-only?", ""},
		{[]string{"send", chat, "--read-only"}, "--read-only is a global option; put it before the command", ""},
		{[]string{"chats", "--error-format=json"}, "--error-format is a global option", ""},
		{[]string{"send", chat, "--text"}, "--text needs a value; run line send --help", ""},
		{[]string{"chats", "--limit", "secret-value"}, "invalid value for --limit; run line chats --help", "secret-value"},
		{[]string{"auth", "stats"}, "did you mean line auth status?", ""},
		{[]string{"help", "chat"}, "did you mean line chats?", ""},
	} {
		a, out, diagnostics := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("invalid arguments opened the session"); return nil, nil }
		err := a.Run(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.not != "" && strings.Contains(err.Error(), tc.not)) || strings.Contains(err.Error(), "\n") {
			t.Fatalf("%q: got %v, want %q", tc.args, err, tc.want)
		}
		if out.Len() != 0 || diagnostics.Len() != 0 {
			t.Fatalf("%q printed usage: %q %q", tc.args, out.String(), diagnostics.String())
		}
	}
	a, _, _ := testApp(nil)
	var machine *MachineError
	if err := a.Run([]string{"--error-format=json", "chats", "--limt"}); !errors.As(err, &machine) || machine.Detail.Code != "invalid_arguments" {
		t.Fatalf("JSON flag error %v", err)
	}
}

func TestEditDistanceAndClosest(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"chat", "chats", 1}, {"sned", "send", 1}, {"limt", "limit", 1}, {"", "abc", 3}, {"abc", "abc", 0}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Fatalf("%s/%s: %d", tc.a, tc.b, got)
		}
	}
	if closest("x", []string{"xy", "help"}) != "" || closest("unred", []string{"unread", "all"}) != "unread" {
		t.Fatal("closest picked a weak match")
	}
}
