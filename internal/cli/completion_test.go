package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var helpFlag = regexp.MustCompile(`(?m)^\s+--?([a-z][a-z0-9-]*)\b`)

// helpFlags runs COMMAND --help and returns the long flags it documents.
func helpFlags(t *testing.T, words []string) []string {
	t.Helper()
	a, out, diagnostics := testApp(nil)
	a.Lock = func() (func(), error) { t.Fatal("help opened the session"); return nil, nil }
	if err := a.Run(append(append([]string{}, words...), "--help")); err != nil {
		t.Fatalf("%q --help: %v", words, err)
	}
	seen := make(map[string]bool)
	var flags []string
	for _, m := range helpFlag.FindAllStringSubmatch(out.String()+diagnostics.String(), -1) {
		if name := m[1]; name != "help" && name != "h" && !seen[name] {
			seen[name] = true
			flags = append(flags, name)
		}
	}
	slices.Sort(flags)
	return flags
}

func TestCompletionSpecMatchesHelp(t *testing.T) {
	for _, c := range completionKeys(completionSpec()) {
		if c.name == "help" || c.name == "version" {
			continue
		}
		got := helpFlags(t, strings.Fields(c.name))
		want := slices.Clone(c.flags)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: --help documents %v, completion has %v", c.name, got, want)
		}
	}
	// Every command in line help has completion, and vice versa.
	a, out, _ := testApp(nil)
	if err := a.Run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
	listed := regexp.MustCompile(`(?m)^  ([a-z]+)\s{2,}`).FindAllStringSubmatch(out.String(), -1)
	var documented []string
	for _, m := range listed {
		documented = append(documented, m[1])
	}
	names := commandNames(completionSpec())
	slices.Sort(documented)
	slices.Sort(names)
	if !slices.Equal(documented, names) {
		t.Fatalf("line help lists %v; completion has %v", documented, names)
	}
}

func TestCompletionScriptsCoverEveryFlag(t *testing.T) {
	keys := completionKeys(completionSpec())
	for _, shell := range completionShells {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range keys {
			var block string
			switch shell {
			case "bash":
				block = lineContaining(script, `"`+c.name+`") opts=`)
			case "zsh":
				block = lineContaining(script, `('`+c.name+`') opts=`)
			case "fish":
				block = strings.Join(linesContaining(script, `'__line_key_is "`+c.name+`"' -l `), "\n")
			case "powershell":
				block = lineContaining(script, `'`+c.name+`' = @('--`)
			}
			if block == "" {
				t.Fatalf("%s: no entry for %q", shell, c.name)
			}
			for _, f := range append(slices.Clone(c.flags), "help") {
				token := "--" + f
				switch shell {
				case "fish":
					token = "-l " + f
				case "powershell":
					token = "'--" + f + "'"
				}
				if !strings.Contains(block+"\n", token+" ") && !strings.Contains(block+"\n", token+"\n") && !strings.Contains(block, token+",") &&
					!strings.Contains(block, token+")") && !strings.Contains(block, token+`"`) {
					t.Errorf("%s: %q lacks %s", shell, c.name, token)
				}
			}
		}
		for _, name := range commandNames(completionSpec()) {
			if !strings.Contains(script, name) {
				t.Errorf("%s: missing command %s", shell, name)
			}
		}
	}
	if _, err := completionScript("tcsh"); err == nil {
		t.Fatal("accepted unsupported shell")
	}
}

func lineContaining(s, needle string) string {
	if lines := linesContaining(s, needle); len(lines) > 0 {
		return lines[0]
	}
	return ""
}

func linesContaining(s, needle string) []string {
	var found []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			found = append(found, line)
		}
	}
	return found
}

func TestCompletionCommandNeedsNoSession(t *testing.T) {
	for _, shell := range completionShells {
		a, out := sealedApp(t, false)
		if err := a.Run([]string{"--read-only", "completion", shell}); err != nil || out.Len() == 0 {
			t.Fatalf("%s: %v", shell, err)
		}
	}
	for _, args := range [][]string{{"completion"}, {"completion", "tcsh"}, {"completion", "bash", "zsh"}, {"completion", "--bogus"}} {
		a, _ := sealedApp(t, false)
		if err := a.Run(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func writeScript(t *testing.T, shell string) string {
	t.Helper()
	script, err := completionScript(shell)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "line."+shell)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBashCompletionBehavior(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("bash unavailable (Windows PATH bash may be the WSL launcher)")
	}
	path := writeScript(t, "bash")
	if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax: %v %s", err, out)
	}
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"line", ""}, "login whoami contacts chats members invites messages send read logout auth watch download react unsend doctor completion version update help"},
		{[]string{"line", "--read-only", "se"}, "send"},
		{[]string{"line", "--error-format", "j"}, "json"},
		{[]string{"line", "--error-format", "json", "--read-only", "rea"}, "read react"},
		{[]string{"line", "--"}, "--error-format --read-only --help --version"},
		{[]string{"line", "send", "Alice", "--t"}, "--timeout --text"},
		{[]string{"line", "chats", "--un"}, "--unread"},
		{[]string{"line", "auth", ""}, "status migrate"},
		{[]string{"line", "auth", "status", "--"}, "--check --json --help"},
		{[]string{"line", "invites", "accept", "--j"}, "--json"},
		{[]string{"line", "react", "x", "--reaction", "l"}, "like love laugh"},
		{[]string{"line", "completion", "f"}, "fish"},
		{[]string{"line", "help", "inv"}, "invites"},
	} {
		var script bytes.Buffer
		script.WriteString("source " + shellQuote(path) + "\nCOMP_WORDS=(")
		for _, w := range tc.words {
			script.WriteString(shellQuote(w) + " ")
		}
		script.WriteString(")\nCOMP_CWORD=" + strconv.Itoa(len(tc.words)-1) + "\n_line_completion\necho \"${COMPREPLY[*]}\"\n")
		out, err := exec.Command(bash, "--norc", "--noprofile", "-c", script.String()).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Errorf("%q: got %q (%v), want %q", tc.words, strings.TrimSpace(string(out)), err, tc.want)
		}
	}
}

func TestZshCompletionBehavior(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("zsh unavailable")
	}
	path := writeScript(t, "zsh")
	if out, err := exec.Command(zsh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("zsh syntax: %v %s", err, out)
	}
	// Stub the completion builtins to print what _line would offer.
	stubs := "compdef() { :; }\ncompadd() { [[ $1 == -- ]] && shift; print -r -- \"$*\"; }\n" +
		"_describe() { print -r -- \"${(j: :)${(@)${(P)4}%%:*}}\"; }\n_files() { print -r -- FILES; }\n"
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"line", ""}, "login whoami contacts chats members invites messages send read logout auth watch download react unsend doctor completion version update help"},
		{[]string{"line", "--"}, "--error-format --read-only --help --version"},
		{[]string{"line", "--read-only", "send", "Alice", "--"}, "--timeout --json --show-ids --require-e2ee --mentions --text --stdin --file --image --reply-to --help"},
		{[]string{"line", "send", "Alice", "--file", ""}, "FILES"},
		{[]string{"line", "--error-format", "json", "auth", ""}, "status migrate"},
		{[]string{"line", "invites", "reject", "--"}, "--timeout --json --help"},
		{[]string{"line", "react", "x", "--reaction", ""}, "like love laugh surprise sad angry"},
	} {
		var script strings.Builder
		script.WriteString(stubs + "source " + shellQuote(path) + "\nwords=(")
		for _, w := range tc.words {
			script.WriteString(shellQuote(w) + " ")
		}
		last := tc.words[len(tc.words)-1]
		script.WriteString(")\nCURRENT=" + strconv.Itoa(len(tc.words)) + "\nPREFIX=" + shellQuote(last) + "\n_line\n")
		out, err := exec.Command(zsh, "-f", "-c", script.String()).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Errorf("%q: got %q (%v), want %q", tc.words, strings.TrimSpace(string(out)), err, tc.want)
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
