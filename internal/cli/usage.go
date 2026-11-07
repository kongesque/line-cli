package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// commandHelp is one command's help page. Help goes to stdout; errors print
// one line to stderr with a pointer back to the help.
type commandHelp struct {
	Command  string // "send", "auth status"
	Usage    []string
	About    string
	Groups   []flagGroup
	Examples []string
}

type flagGroup struct {
	Title string
	Flags []string
}

// Placeholders shown after flags that take a value.
var flagPlaceholders = map[string]string{
	"text": "TEXT", "file": "PATH", "image": "PATH", "output": "PATH", "message": "ID", "reply-to": "ID",
	"timeout": "DURATION", "limit": "N", "search": "NAME", "mid": "MID", "cursor": "CURSOR", "mentions": "JSON",
	"reaction": "REACTION", "email": "ADDRESS", "storage": "BACKEND",
}

// newFlagSet returns a flag set that never prints; parseFlags reports help
// and errors itself.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseFlags parses args. With -h/--help it prints help to stdout and returns
// shown=true. Any other parse error becomes one line with a suggestion.
func (a *App) parseFlags(fs *flag.FlagSet, h commandHelp, args []string) (shown bool, err error) {
	err = fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return true, a.printHelp(fs, h)
	}
	if err != nil {
		return false, flagError(fs, h.Command, err)
	}
	return false, nil
}

func flagError(fs *flag.FlagSet, command string, err error) error {
	pointer := "run line " + command + " --help"
	sentence := "Run line " + command + " --help"
	text := err.Error()
	switch {
	case strings.HasPrefix(text, "flag provided but not defined: "):
		name := strings.TrimLeft(strings.TrimPrefix(text, "flag provided but not defined: "), "-")
		name = safeWord(name)
		if name == "read-only" || name == "error-format" {
			return fmt.Errorf("--%s is a global option; put it before the command, e.g. line --%s %s", name, name, command)
		}
		var known []string
		fs.VisitAll(func(f *flag.Flag) { known = append(known, f.Name) })
		if best := closest(name, known); best != "" {
			return fmt.Errorf("unknown option --%s; did you mean --%s? %s", name, best, sentence)
		}
		return fmt.Errorf("unknown option --%s; %s", name, pointer)
	case strings.HasPrefix(text, "flag needs an argument: "):
		name := safeWord(strings.TrimLeft(strings.TrimPrefix(text, "flag needs an argument: "), "-"))
		return fmt.Errorf("--%s needs a value; %s", name, pointer)
	case strings.HasPrefix(text, "invalid value ") || strings.HasPrefix(text, "invalid boolean value "):
		// Values are never echoed: they may be message text.
		if i := strings.Index(text, " for flag -"); i >= 0 {
			name := text[i+len(" for flag -"):]
			if j := strings.Index(name, ":"); j >= 0 {
				name = name[:j]
			}
			return fmt.Errorf("invalid value for --%s; %s", safeWord(strings.TrimLeft(name, "-")), pointer)
		}
		return fmt.Errorf("invalid option value; %s", pointer)
	}
	return fmt.Errorf("invalid options; %s", pointer)
}

// safeWord bounds and sanitizes user input echoed in an error.
func safeWord(s string) string {
	s = terminalText(s)
	if utf8.RuneCountInString(s) > 40 {
		s = string([]rune(s)[:40]) + "…"
	}
	return s
}

func (a *App) printHelp(fs *flag.FlagSet, h commandHelp) error {
	var b strings.Builder
	for i, line := range h.Usage {
		if i == 0 {
			b.WriteString("Usage: " + line + "\n")
		} else {
			b.WriteString("       " + line + "\n")
		}
	}
	if h.About != "" {
		b.WriteString("\n" + strings.TrimSpace(h.About) + "\n")
	}
	listed := make(map[string]bool)
	groups := append([]flagGroup(nil), h.Groups...)
	for _, g := range groups {
		for _, name := range g.Flags {
			listed[name] = true
		}
	}
	var rest []string
	fs.VisitAll(func(f *flag.Flag) {
		if !listed[f.Name] {
			rest = append(rest, f.Name)
		}
	})
	if len(rest) > 0 {
		groups = append(groups, flagGroup{"Options", rest})
	}
	type row struct{ left, right string }
	var rows [][]row
	width := len("-h, --help")
	for _, g := range groups {
		var rs []row
		for _, name := range g.Flags {
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			left := "--" + f.Name
			if !isBoolFlag(f) {
				placeholder := flagPlaceholders[f.Name]
				if placeholder == "" {
					placeholder = "VALUE"
				}
				left += " " + placeholder
			}
			right := f.Usage
			if d := f.DefValue; d != "" && d != "false" && d != "0" && d != "0s" && d != "[]" {
				right += " (default " + d + ")"
			}
			width = max(width, len(left))
			rs = append(rs, row{left, right})
		}
		rows = append(rows, rs)
	}
	for i, g := range groups {
		if len(rows[i]) == 0 {
			continue
		}
		b.WriteString("\n" + g.Title + ":\n")
		for _, r := range rows[i] {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, r.left, r.right)
		}
	}
	fmt.Fprintf(&b, "\n  %-*s  %s\n", width, "-h, --help", "show this help")
	if len(h.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range h.Examples {
			b.WriteString("  " + e + "\n")
		}
	}
	_, err := io.WriteString(a.Out, b.String())
	return err
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// closest returns the candidate within a small edit distance of word, or "".
func closest(word string, candidates []string) string {
	if len(word) < 2 {
		return ""
	}
	best, bestDistance := "", 3
	for _, c := range candidates {
		d := editDistance(strings.ToLower(word), c)
		if strings.HasPrefix(c, strings.ToLower(word)) && len(word) >= 3 {
			d = min(d, 1)
		}
		if d < bestDistance && d < len(word) {
			best, bestDistance = c, d
		}
	}
	return best
}

// editDistance is the optimal string alignment distance (adjacent
// transpositions count as one edit).
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
