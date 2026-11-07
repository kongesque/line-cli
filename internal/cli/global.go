package cli

import (
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
)

// ReadOnlyEnv names the environment switch for read-only mode.
const ReadOnlyEnv = "LINE_CLI_READ_ONLY"

// GlobalOptions are accepted only before the command, in any order, so message
// text and command arguments are never mistaken for them.
type GlobalOptions struct {
	ErrorFormat string // "text" (default) or "json"
	ReadOnly    bool
}

// ParseGlobalOptions consumes leading global options and returns the command
// arguments. Parsing continues across all leading global options so a later
// valid --error-format=json still selects JSON for an earlier invalid value.
// errorFormatValid is false only when --error-format itself was malformed;
// that error is always reported as text.
func ParseGlobalOptions(args []string) (options GlobalOptions, rest []string, errorFormatValid bool, err error) {
	options.ErrorFormat = "text"
	errorFormatValid = true
	seenFormat, seenReadOnly := false, false
	fail := func(message string) {
		if err == nil {
			err = fault.New("invalid_arguments", "validation", message, nil)
		}
	}
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--error-format" || strings.HasPrefix(arg, "--error-format="):
			value := strings.TrimPrefix(arg, "--error-format=")
			if arg == "--error-format" {
				if len(args) < 2 {
					errorFormatValid = false
					fail("--error-format requires text or json")
					args = args[1:]
					continue
				}
				value, args = args[1], args[1:]
			}
			args = args[1:]
			if seenFormat {
				fail("--error-format may be given only once")
				continue
			}
			seenFormat = true
			if value != "text" && value != "json" {
				errorFormatValid = false
				fail("--error-format must be text or json")
				continue
			}
			options.ErrorFormat = value
		case arg == "--read-only" || strings.HasPrefix(arg, "--read-only="):
			args = args[1:]
			if seenReadOnly {
				fail("--read-only may be given only once")
				continue
			}
			seenReadOnly = true
			if arg == "--read-only" {
				options.ReadOnly = true
				continue
			}
			// The flag can only enable read-only mode; it never disables the
			// environment switch.
			enabled, ok := parseSwitch(strings.TrimPrefix(arg, "--read-only="))
			if !ok {
				fail("--read-only accepts no value, or one of true, false, 1, or 0")
				continue
			}
			options.ReadOnly = enabled
		default:
			return options, args, errorFormatValid, err
		}
	}
	return options, args, errorFormatValid, err
}

// ParseReadOnlyEnv interprets LINE_CLI_READ_ONLY. Unset or empty is off; any
// value other than 1, true, 0, or false is rejected instead of being ignored.
func ParseReadOnlyEnv(value string) (bool, error) {
	enabled, ok := parseSwitch(value)
	if value == "" {
		return false, nil
	}
	if !ok {
		return false, fault.New("invalid_arguments", "validation", ReadOnlyEnv+" must be 1, true, 0, or false", nil)
	}
	return enabled, nil
}

func parseSwitch(value string) (enabled, ok bool) {
	switch strings.ToLower(value) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	}
	return false, false
}

// refuseReadOnly runs after option parsing, so help still works, and before
// prompts, input, storage, locks, the network, or sequence reservation.
func (a *App) refuseReadOnly(action string) error {
	if !a.readOnly {
		return nil
	}
	a.errorStage = "preparation"
	return session.ReadOnlyError(action)
}
