package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// completionCommand is the single source for every completion script. A drift
// test compares it with each command's --help output.
type completionCommand struct {
	name, about string
	flags       []string // long names without dashes; --help is added everywhere
	args        []string // positional words (subcommands or fixed values)
	subs        []completionCommand
}

var completionShells = []string{"bash", "zsh", "fish", "powershell"}

// Flags that take a value, and the fixed values some of them accept.
var (
	completionValueFlags = map[string]bool{"timeout": true, "email": true, "search": true, "limit": true, "mid": true, "cursor": true,
		"mentions": true, "text": true, "file": true, "image": true, "reply-to": true, "message": true, "reaction": true,
		"output": true, "storage": true, "error-format": true}
	completionFileFlags = []string{"file", "image", "output"}
	completionValues    = []struct {
		flag   string
		values []string
	}{
		{"error-format", []string{"text", "json"}},
		{"reaction", []string{"like", "love", "laugh", "surprise", "sad", "angry"}},
		{"storage", []string{"headless"}},
	}
	completionGlobal = []struct{ flag, about string }{
		{"error-format", "Structured errors on stderr (text or json)"},
		{"read-only", "Refuse sends and other changes"},
		{"help", "Show help"},
		{"version", "Show the installed version"},
	}
)

func completionSpec() []completionCommand {
	invite := []string{"timeout", "json"}
	commands := []completionCommand{
		{name: "login", about: "Sign in by QR code or email", flags: []string{"email", "qr", "qr-url", "force", "headless"}},
		{name: "whoami", about: "Show your signed-in account", flags: []string{"timeout", "show-ids", "json"}},
		{name: "contacts", about: "Find friends or look up people", flags: []string{"timeout", "search", "limit", "mid", "show-ids", "json"}},
		{name: "chats", about: "Browse recent conversations", flags: []string{"timeout", "all", "search", "limit", "show-ids", "unread", "details", "json"}},
		{name: "members", about: "List group members and invitees", flags: []string{"json", "timeout"}},
		{name: "invites", about: "List, accept, or reject group invitations", flags: invite, subs: []completionCommand{
			{name: "accept", about: "Accept a group invitation", flags: invite},
			{name: "reject", about: "Reject a group invitation", flags: invite},
		}},
		{name: "messages", about: "Choose a chat and read messages", flags: []string{"timeout", "json", "show-ids", "diagnostics", "details", "resolve-names", "limit", "page", "cursor"}},
		{name: "send", about: "Choose a recipient and write a message", flags: []string{"timeout", "json", "show-ids", "require-e2ee", "mentions", "text", "stdin", "file", "image", "reply-to"}},
		{name: "read", about: "Mark a chat as read (others see Read)", flags: []string{"timeout", "message", "json", "show-ids"}},
		{name: "logout", about: "Sign out on this device"},
		{name: "auth", about: "Inspect or migrate local session storage", subs: []completionCommand{
			{name: "status", about: "Inspect local session storage", flags: []string{"check", "json"}},
			{name: "migrate", about: "Move a Linux session to headless storage", flags: []string{"storage"}},
		}},
		{name: "watch", about: "Stream live events", flags: []string{"json", "verbose", "from-now", "limit", "timeout"}},
		{name: "download", about: "Save an attachment", flags: []string{"timeout", "message", "output", "json"}},
		{name: "react", about: "Add or remove a reaction", flags: []string{"timeout", "message", "json", "reaction", "remove"}},
		{name: "unsend", about: "Retract one of your own messages", flags: []string{"timeout", "message", "json"}},
		{name: "doctor", about: "Check installation, storage, sign-in, and more", flags: []string{"offline", "json", "timeout"}},
		{name: "completion", about: "Print a shell completion script", args: completionShells},
		{name: "version", about: "Show the installed version"},
		{name: "update", about: "Update LINE CLI or show upgrade steps", flags: []string{"check", "json"}},
		{name: "help", about: "Show help"},
	}
	names := make([]string, 0, len(commands))
	for i := range commands {
		names = append(names, commands[i].name)
		for _, sub := range commands[i].subs {
			commands[i].args = append(commands[i].args, sub.name)
		}
	}
	commands[len(commands)-1].args = names
	return commands
}

// completionKeys flattens the spec into "cmd" and "cmd sub" entries.
func completionKeys(spec []completionCommand) []completionCommand {
	var keys []completionCommand
	for _, c := range spec {
		keys = append(keys, c)
		for _, sub := range c.subs {
			sub.name = c.name + " " + sub.name
			keys = append(keys, sub)
		}
	}
	return keys
}

func dashed(flags []string) []string {
	out := make([]string, 0, len(flags)+1)
	for _, f := range flags {
		out = append(out, "--"+f)
	}
	return append(out, "--help")
}

func (a *App) completionCommand(args []string) error {
	fs := newFlagSet("completion")
	h := commandHelp{Command: "completion", Usage: []string{"line completion bash|zsh|fish|powershell"},
		About:    "Print a static completion script for commands, subcommands, and options.\nIt reads no session and makes no network requests. Regenerate it after\nupgrading. See the CLI guide for install steps.",
		Examples: []string{"line completion zsh > ~/.zfunc/_line", "line completion fish > ~/.config/fish/completions/line.fish"}}
	var shell string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		shell, args = args[0], args[1:]
	}
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if fs.NArg() != 0 || shell == "" {
		return errors.New("choose one shell: line completion bash|zsh|fish|powershell")
	}
	script, err := completionScript(shell)
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.Out, script)
	return err
}

func completionScript(shell string) (string, error) {
	spec := completionSpec()
	switch shell {
	case "bash":
		return bashCompletion(spec), nil
	case "zsh":
		return zshCompletion(spec), nil
	case "fish":
		return fishCompletion(spec), nil
	case "powershell":
		return powershellCompletion(spec), nil
	}
	return "", errors.New("unsupported shell; choose bash, zsh, fish, or powershell")
}

func commandNames(spec []completionCommand) []string {
	names := make([]string, 0, len(spec))
	for _, c := range spec {
		names = append(names, c.name)
	}
	return names
}

func globalFlags() []string {
	flags := make([]string, 0, len(completionGlobal))
	for _, g := range completionGlobal {
		flags = append(flags, "--"+g.flag)
	}
	return flags
}

func bashCompletion(spec []completionCommand) string {
	var b strings.Builder
	b.WriteString(`# bash completion for line (LINE CLI). Generated by: line completion bash
_line_completion() {
    local cur prev cmd sub word i
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev=""
    if [ "$COMP_CWORD" -gt 0 ]; then prev="${COMP_WORDS[COMP_CWORD-1]}"; fi
    cmd="" sub="" i=1
    while [ "$i" -lt "$COMP_CWORD" ]; do
        word="${COMP_WORDS[i]}"
        if [ -z "$cmd" ]; then
            case "$word" in
                --error-format) i=$((i + 1)) ;;
                -*) ;;
                *) cmd="$word" ;;
            esac
        elif [ -z "$sub" ] && [ "${word#-}" = "$word" ]; then
            sub="$word"
            break
        fi
        i=$((i + 1))
    done
    case "$prev" in
`)
	for _, v := range completionValues {
		fmt.Fprintf(&b, "        --%s) COMPREPLY=( $(compgen -W %q -- \"$cur\") ); return 0 ;;\n", v.flag, strings.Join(v.values, " "))
	}
	fmt.Fprintf(&b, "        %s) compopt -o filenames 2>/dev/null; COMPREPLY=( $(compgen -f -- \"$cur\") ); return 0 ;;\n", strings.Join(dashed(completionFileFlags)[:len(completionFileFlags)], "|"))
	b.WriteString("    esac\n    if [ -z \"$cmd\" ]; then\n")
	fmt.Fprintf(&b, "        case \"$cur\" in -*) COMPREPLY=( $(compgen -W %q -- \"$cur\") ) ;; *) COMPREPLY=( $(compgen -W %q -- \"$cur\") ) ;; esac\n        return 0\n    fi\n",
		strings.Join(globalFlags(), " "), strings.Join(commandNames(spec), " "))
	b.WriteString(`    local key="$cmd" opts="" args=""
    case "$cmd" in auth|invites) if [ -n "$sub" ]; then key="$cmd $sub"; fi ;; esac
    case "$key" in
`)
	for _, c := range completionKeys(spec) {
		fmt.Fprintf(&b, "        %q) opts=%q args=%q ;;\n", c.name, strings.Join(dashed(c.flags), " "), strings.Join(c.args, " "))
	}
	b.WriteString(`    esac
    case "$cur" in
        -*) COMPREPLY=( $(compgen -W "$opts" -- "$cur") ) ;;
        *) COMPREPLY=( $(compgen -W "$args" -- "$cur") ) ;;
    esac
    return 0
}
complete -F _line_completion line
`)
	return b.String()
}

func zshCompletion(spec []completionCommand) string {
	var b strings.Builder
	b.WriteString("#compdef line\n# zsh completion for line (LINE CLI). Generated by: line completion zsh\n_line() {\n    local -a commands opts args\n    commands=(\n")
	for _, c := range spec {
		fmt.Fprintf(&b, "        %s\n", zshQuote(c.name+":"+c.about))
	}
	b.WriteString(`    )
    local cmd="" sub="" word prev key
    local -i i=2
    while (( i < CURRENT )); do
        word=${words[i]}
        if [[ -z $cmd ]]; then
            case $word in
                --error-format) (( i++ )) ;;
                -*) ;;
                *) cmd=$word ;;
            esac
        elif [[ -z $sub && $word != -* ]]; then
            sub=$word
            break
        fi
        (( i++ ))
    done
    prev=${words[CURRENT-1]}
    case $prev in
`)
	for _, v := range completionValues {
		fmt.Fprintf(&b, "        --%s) compadd -- %s; return ;;\n", v.flag, strings.Join(v.values, " "))
	}
	fmt.Fprintf(&b, "        %s) _files; return ;;\n", strings.Join(dashed(completionFileFlags)[:len(completionFileFlags)], "|"))
	b.WriteString("    esac\n    if [[ -z $cmd ]]; then\n")
	fmt.Fprintf(&b, "        if [[ $PREFIX == -* ]]; then compadd -- %s; else _describe -t commands 'line command' commands; fi\n        return\n    fi\n", strings.Join(globalFlags(), " "))
	b.WriteString(`    key=$cmd
    if [[ ( $cmd == auth || $cmd == invites ) && -n $sub ]]; then key="$cmd $sub"; fi
    case $key in
`)
	for _, c := range completionKeys(spec) {
		fmt.Fprintf(&b, "        (%s) opts=(%s); args=(%s) ;;\n", zshQuote(c.name), strings.Join(dashed(c.flags), " "), strings.Join(c.args, " "))
	}
	b.WriteString(`    esac
    if [[ $PREFIX == -* ]]; then
        compadd -- $opts
    elif (( ${#args} )); then
        compadd -- $args
    fi
}
if [[ "${funcstack[1]}" == _line ]]; then
    _line "$@"
else
    compdef _line line
fi
`)
	return b.String()
}

func zshQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func fishCompletion(spec []completionCommand) string {
	var b strings.Builder
	b.WriteString(`# fish completion for line (LINE CLI). Generated by: line completion fish
function __line_key
    set -l tokens (commandline -opc)
    set -e tokens[1]
    set -l cmd ""
    set -l skip 0
    for t in $tokens
        if test $skip -eq 1
            set skip 0
            continue
        end
        if test -z "$cmd"
            switch $t
                case --error-format
                    set skip 1
                case '-*'
                case '*'
                    set cmd $t
            end
        else if not string match -q -- '-*' $t
            switch $cmd
                case auth invites
                    echo "$cmd $t"
                    return 0
            end
            break
        end
    end
    echo $cmd
end
function __line_key_is
    set -l key (__line_key)
    test "$key" = "$argv[1]"
end
complete -c line -f
`)
	for _, g := range completionGlobal {
		extra := ""
		if completionValueFlags[g.flag] {
			extra = " -x -a 'text json'"
		}
		fmt.Fprintf(&b, "complete -c line -n '__line_key_is \"\"' -l %s%s -d %s\n", g.flag, extra, fishQuote(g.about))
	}
	for _, c := range spec {
		fmt.Fprintf(&b, "complete -c line -n '__line_key_is \"\"' -a %s -d %s\n", c.name, fishQuote(c.about))
	}
	values := make(map[string]string)
	for _, v := range completionValues {
		values[v.flag] = strings.Join(v.values, " ")
	}
	files := make(map[string]bool)
	for _, f := range completionFileFlags {
		files[f] = true
	}
	for _, c := range completionKeys(spec) {
		cond := fmt.Sprintf("'__line_key_is %s'", fishQuote(c.name))
		for _, f := range append(append([]string{}, c.flags...), "help") {
			opt := ""
			switch {
			case values[f] != "":
				opt = " -x -a " + fishQuote(values[f])
			case files[f]:
				opt = " -r -F"
			case completionValueFlags[f]:
				opt = " -x"
			}
			fmt.Fprintf(&b, "complete -c line -n %s -l %s%s\n", cond, f, opt)
		}
		if len(c.args) > 0 {
			fmt.Fprintf(&b, "complete -c line -n %s -a %s\n", cond, fishQuote(strings.Join(c.args, " ")))
		}
	}
	return b.String()
}

// fishQuote uses double quotes so it nests inside single-quoted conditions.
func fishQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, `'`, `\'`).Replace(s) + `"`
}

func powershellCompletion(spec []completionCommand) string {
	var b strings.Builder
	b.WriteString("# PowerShell completion for line (LINE CLI). Generated by: line completion powershell\n")
	b.WriteString("Register-ArgumentCompleter -Native -CommandName 'line', 'line.exe' -ScriptBlock {\n    param($wordToComplete, $commandAst, $cursorPosition)\n")
	b.WriteString("    $commands = @(" + psList(commandNames(spec)) + ")\n")
	b.WriteString("    $globals = @(" + psList(globalFlags()) + ")\n")
	b.WriteString("    $options = @{\n")
	for _, c := range completionKeys(spec) {
		fmt.Fprintf(&b, "        %s = @(%s)\n", psQuote(c.name), psList(dashed(c.flags)))
	}
	b.WriteString("    }\n    $positional = @{\n")
	for _, c := range completionKeys(spec) {
		if len(c.args) > 0 {
			fmt.Fprintf(&b, "        %s = @(%s)\n", psQuote(c.name), psList(c.args))
		}
	}
	b.WriteString("    }\n    $values = @{\n")
	for _, v := range completionValues {
		fmt.Fprintf(&b, "        %s = @(%s)\n", psQuote("--"+v.flag), psList(v.values))
	}
	b.WriteString(`    }
    $words = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.ToString() })
    if ($wordToComplete -ne '' -and $words.Count -gt 0) {
        if ($words.Count -eq 1) { $words = @() } else { $words = $words[0..($words.Count - 2)] }
    }
    $cmd = ''; $sub = ''; $skip = $false
    foreach ($w in $words) {
        if ($skip) { $skip = $false; continue }
        if ($cmd -eq '') {
            if ($w -eq '--error-format') { $skip = $true }
            elseif (-not $w.StartsWith('-')) { $cmd = $w }
        } elseif ($sub -eq '' -and -not $w.StartsWith('-')) { $sub = $w; break }
    }
    $prev = if ($words.Count -gt 0) { $words[$words.Count - 1] } else { '' }
    if ($values.ContainsKey($prev)) {
        $candidates = $values[$prev]
    } elseif ($cmd -eq '') {
        $candidates = if ($wordToComplete.StartsWith('-')) { $globals } else { $commands }
    } else {
        $key = $cmd
        if (($cmd -eq 'auth' -or $cmd -eq 'invites') -and $sub -ne '') { $key = "$cmd $sub" }
        if ($wordToComplete.StartsWith('-')) { $candidates = $options[$key] }
        else { $candidates = $positional[$key] }
    }
    @($candidates) | Where-Object { $_ -and $_ -like "$wordToComplete*" } | ForEach-Object {
        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
    }
}
`)
	return b.String()
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func psList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = psQuote(item)
	}
	return strings.Join(quoted, ", ")
}
