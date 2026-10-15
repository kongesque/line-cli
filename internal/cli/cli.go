package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const help = `LINE — your conversations from the terminal

Usage: line [--error-format text|json] <command> [options]

Everyday commands:
  login       Sign in by QR code (--email ADDRESS for email login)
  whoami      Show your signed-in account
  contacts    Find friends (--search NAME) or look up people (--mid MID)
  chats       Browse recent conversations
  messages    Choose a chat and read messages
  send        Choose a recipient and write a message
  logout      Sign out on this device

More commands:
  auth        Inspect local session storage (auth status)
  watch       Stream live events (--json)
  download    Save an image, video, audio, or file attachment
  react       Add or remove a reaction
  unsend      Retract one of your own messages
  version     Show the installed version
  update      Update LINE CLI or show upgrade instructions (--check to preview)
  help        Show this help

Examples:
  line messages "Alice"
  line send "Alice" --text "Hello!"
  line send "Alice" --file report.pdf
  line chats --search "Family"

whoami, contacts, chats, messages, send, download, react, and unsend accept --timeout.
Use --error-format=json before a core command or auth status for JSON errors on stderr.
This disables prompts and preserves stdout success/partial-result formats.
In a terminal, missing details are prompted for. Ctrl-C cancels.
Use --show-ids for IDs, --json for scripts, or COMMAND --help for all options.
`

type App struct {
	JSONErrors       bool
	errorStage       string
	errorMutation    *fault.Mutation
	Interactive      bool
	StdoutIsTerminal bool
	input            *bufio.Reader
	promptAccount    *promptAccount
	loginSnapshot    *session.LoginSnapshot
	LoginQR          func(context.Context, func(session.QRLoginEvent) error) (*line.Profile, error)
	RenderQR         QRRenderer
	QRTerminal       func() QRTerminal
	Context          context.Context
	WatchLock        func() (func(), error)
	In               io.Reader
	Out              io.Writer
	Err              io.Writer
	Manager          *session.Manager
	Lock             func() (func(), error)
	Password         func() (string, error)
	Continue         func() error
	Version          string
	Distribution     string
	Updater          updater
	NewHeadlessLogin func() (session.LoginStorage, error)
	MigrateHeadless  func(bool) error
}

// Run validates command arguments before accessing Keychain or the network.
func (a *App) run(args []string) error {
	a.input, a.promptAccount, a.loginSnapshot = nil, nil, nil
	if len(args) == 0 {
		_, err := io.WriteString(a.Out, help)
		return err
	}
	command := args[0]
	if command == "--help" || command == "-h" {
		command = "help"
	}
	if command == "--version" {
		command = "version"
	}
	switch command {
	case "update":
		return a.updateCommand(args[1:])
	case "login":
		return a.loginCommand(args[1:])
	case "auth":
		return a.authCommand(args[1:])
	case "download":
		return a.downloadCommand(args[1:])
	case "react", "unsend":
		return a.actionCommand(command, args[1:])
	case "watch":
		return a.watchCommand(args[1:])
	case "contacts":
		return a.contactsCommand(args[1:])
	case "chats":
		return a.chatCommand(args[1:])
	case "messages", "send":
		return a.messageCommand(command, args[1:])
	case "help", "version":
		if len(args) != 1 {
			return errors.New("unexpected arguments; run line help")
		}
		if command == "version" {
			_, err := fmt.Fprintln(a.Out, "line "+a.Version)
			return err
		}
		_, err := io.WriteString(a.Out, help)
		return err
	case "whoami", "logout":
	default:
		return errors.New("unknown command; run line help")
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var timeout time.Duration
	if command == "whoami" {
		fs.DurationVar(&timeout, "timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	}
	fs.Usage = func() { fmt.Fprintf(a.Err, "Usage: line %s [options]\n", command); fs.PrintDefaults() }
	var jsonOutput bool
	var showIDs bool
	if command == "whoami" {
		fs.BoolVar(&showIDs, "show-ids", false, "show full IDs")
		fs.BoolVar(&jsonOutput, "json", false, "write JSON to stdout")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line " + command + " --help")
	}
	restore, err := a.commandDeadline(timeout)
	if err != nil {
		return err
	}
	defer restore()
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line " + command + " --help")
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if command == "logout" {
		if err := a.Manager.Store.Delete(); err != nil {
			return err
		}
		_, err := fmt.Fprintln(a.Out, "Signed out on this device. Your LINE account remains active on your phone.")
		return err
	}

	switch command {
	case "whoami":
		var profile *line.Profile
		err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) { profile, err = api.GetProfileContext(a.loginContext()); return })
		if err != nil {
			return err
		}
		if profile == nil {
			return errors.New("LINE returned no profile")
		}
		if jsonOutput {
			return a.json(profile)
		}
		_, err = fmt.Fprintf(a.Out, "Signed in as %s\nSession saved securely on this device.\n", terminalText(profile.DisplayName))
		if err == nil && showIDs {
			_, err = fmt.Fprintf(a.Out, "ID: %s\n", terminalText(profile.Mid))
		}
		return err
	}
	return nil
}

func (a *App) loginEmail(email string) error {
	if err := a.prepareLogin(); err != nil {
		return err
	}
	password, err := a.Password()
	if err != nil {
		return err
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := a.Manager.CheckLogin(a.loginContext(), a.loginSnapshot); err != nil {
		return err
	}
	profile, err := a.Manager.LoginContext(a.loginContext(), email, password, func(pin string, wait bool) error {
		if pin != "" {
			fmt.Fprintf(a.Err, "Open LINE on your phone and enter PIN: %s\n", terminalText(pin))
		} else {
			fmt.Fprintln(a.Err, "Open LINE on your phone and approve this login.")
		}
		if wait {
			return a.Continue()
		}
		fmt.Fprintln(a.Err, "Waiting for phone verification…")
		return nil
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.Out, "Signed in as %s. Session saved securely.\nNext: line chats\n", terminalText(profile.DisplayName))
	return err
}

func (a *App) loginContext() context.Context {
	if a.Context != nil {
		return a.Context
	}
	return context.Background()
}

func (a *App) prepareLogin() error {
	if a.loginSnapshot != nil {
		return nil
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	a.loginSnapshot, err = a.Manager.PrepareLogin(a.loginContext())
	return err
}

func (a *App) json(value any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

// Chat deliberately excludes LastMessages: this command lists conversations
// without presenting encrypted message payloads as if they were decoded text.
type Chat struct {
	ID          string      `json:"id"`
	Type        int         `json:"type"`
	UnreadCount json.Number `json:"unread_count"`
	Name        string      `json:"name,omitempty"`
	UpdatedAt   int64       `json:"updated_at,omitempty"`
}

func (a *App) chatBoxes(active, recent bool) ([]Chat, error) {
	result := make([]Chat, 0)
	seen := make(map[string]bool)
	cursors := make(map[string]bool)
	options := line.MessageBoxesOptions{MessageBoxCountLimit: 100, WithUnreadCount: true, ActiveOnly: active}
	if recent {
		options.LastMessagesPerMessageBoxCount = 1
	}
	for {
		var response *line.MessageBoxesResponse
		if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetMessageBoxesContext(a.loginContext(), options)
			return
		}); err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("LINE returned an empty chat response")
		}
		for _, box := range response.MessageBoxes {
			if !seen[box.ID] {
				unread := box.UnreadCount
				if unread == "" {
					unread = "0"
				}
				chat := Chat{ID: box.ID, Type: box.MidType, UnreadCount: unread}
				if recent {
					if box.LastDeliveredMessageID != nil {
						chat.UpdatedAt, _ = box.LastDeliveredMessageID.DeliveredTime.Int64()
					}
					for _, message := range box.LastMessages {
						ts, _ := message.CreatedTime.Int64()
						chat.UpdatedAt = max(chat.UpdatedAt, ts)
					}
				}
				result = append(result, chat)
				seen[box.ID] = true
			}
		}
		if !response.HasNext {
			return result, nil
		}
		if len(response.MessageBoxes) == 0 {
			return nil, errors.New("LINE chat pagination returned an empty page before completion")
		}
		next := response.MessageBoxes[len(response.MessageBoxes)-1].ID
		if next == "" || cursors[next] {
			return nil, errors.New("LINE chat pagination did not advance; retry the command")
		}
		cursors[next] = true
		options.MinChatID = next
	}
}

// LINE names are untrusted text. Prevent terminal escape/control injection while
// preserving Unicode names; JSON output retains the original data, escaped.
func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
