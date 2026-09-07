package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

const help = `Usage: line <command> [options]

Commands:
  login     --email ADDRESS    Sign in with password and phone verification
  whoami    [--json]           Show your LINE profile
  contacts  [--json]           List contacts and their LINE IDs
  chats     [--search NAME]    Show recent conversations by name
  messages  CHAT [--limit N] [--json]  Read recent text messages
  send      CHAT --text TEXT [--json] Send text (--stdin also supported)
  watch     [--json]           Stream live events; Ctrl-C stops
  logout                      Delete the locally saved session
  version                     Print build version
  help                        Show this help

CHAT accepts a full ID or a unique exact chat name (quote names with spaces).
Run line chats --help for search, limits, and IDs.

Session secrets are stored in macOS Keychain. Passwords are never saved.
Login uses LINE's Chrome session and may replace an extension/bridge session.
Media is planned; see PLAN.md.
`

type App struct {
	Context   context.Context
	WatchLock func() (func(), error)
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	Manager   *session.Manager
	Lock      func() (func(), error)
	Password  func() (string, error)
	Continue  func() error
	Version   string
}

// Run validates command arguments before accessing Keychain or the network.
func (a *App) Run(args []string) error {
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
	case "watch":
		return a.watchCommand(args[1:])
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
	case "login", "whoami", "contacts", "logout":
	default:
		return errors.New("unknown command; run line help")
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() { fmt.Fprintf(a.Err, "Usage: line %s [options]\n", command); fs.PrintDefaults() }
	var jsonOutput bool
	var email string
	if command == "login" {
		fs.StringVar(&email, "email", "", "LINE account email (required)")
	}
	if command == "whoami" || command == "contacts" {
		fs.BoolVar(&jsonOutput, "json", false, "write JSON to stdout")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line " + command + " --help")
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line " + command + " --help")
	}
	if command == "login" && strings.TrimSpace(email) == "" {
		return errors.New("login requires --email ADDRESS")
	}
	unlock, err := a.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if command == "logout" {
		if err := a.Manager.Store.Delete(); err != nil {
			return err
		}
		_, err := fmt.Fprintln(a.Out, "Local session removed. This does not revoke the session on LINE.")
		return err
	}
	if command == "login" {
		return a.login(strings.TrimSpace(email))
	}
	switch command {
	case "whoami":
		var profile *line.Profile
		err := a.Manager.Do(func(api session.API) (err error) { profile, err = api.GetProfile(); return })
		if err != nil {
			return err
		}
		if profile == nil {
			return errors.New("LINE returned no profile")
		}
		if jsonOutput {
			return a.json(profile)
		}
		_, err = fmt.Fprintf(a.Out, "%s\nID: %s\n", terminalText(profile.DisplayName), terminalText(profile.Mid))
		return err
	case "contacts":
		contacts, err := a.contacts()
		if err != nil {
			return err
		}
		if jsonOutput {
			return a.json(contacts)
		}
		tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME")
		for _, contact := range contacts {
			fmt.Fprintf(tw, "%s\t%s\n", terminalText(contact.Mid), terminalText(contact.EffectiveDisplayName()))
		}
		return tw.Flush()
	}
	return nil
}

func (a *App) login(email string) error {
	fmt.Fprintln(a.Err, "Signing in may replace your existing LINE Chrome extension or bridge session.")
	password, err := a.Password()
	if err != nil {
		return err
	}
	profile, err := a.Manager.Login(email, password, func(pin string, wait bool) error {
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
	_, err = fmt.Fprintf(a.Out, "Signed in as %s (%s). Session saved to macOS Keychain.\n", terminalText(profile.DisplayName), terminalText(profile.Mid))
	return err
}

func (a *App) json(value any) error {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func (a *App) contacts() ([]line.Contact, error) {
	var ids []string
	if err := a.Manager.Do(func(api session.API) (err error) { ids, err = api.GetAllContactIds(); return }); err != nil {
		return nil, err
	}
	result := make([]line.Contact, 0, len(ids))
	seen := make(map[string]bool)
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		var response *line.ContactsResponse
		if err := a.Manager.Do(func(api session.API) (err error) { response, err = api.GetContactsV2(ids[start:end]); return }); err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("LINE returned an empty contacts response")
		}
		for mid, wrapper := range response.Contacts {
			contact := wrapper.Contact
			if contact.Mid == "" {
				contact.Mid = mid
			}
			if !seen[contact.Mid] {
				result = append(result, contact)
				seen[contact.Mid] = true
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i].EffectiveDisplayName(), result[j].EffectiveDisplayName()
		if a == b {
			return result[i].Mid < result[j].Mid
		}
		return a < b
	})
	return result, nil
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
		if err := a.Manager.Do(func(api session.API) (err error) { response, err = api.GetMessageBoxes(options); return }); err != nil {
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
