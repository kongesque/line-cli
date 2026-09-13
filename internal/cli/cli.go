package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const help = `LINE — your conversations from the terminal

Usage: line <command> [options]

Everyday commands:
  login       Sign in with email and phone approval
  whoami      Show your signed-in account
  contacts    Find people (--search NAME)
  chats       Browse recent conversations
  messages    Choose a chat and read messages
  send        Choose a recipient and write a message
  logout      Sign out on this device

More commands:
  auth        Inspect local session storage (auth status)
  watch       Stream live events (--json)
  download    Choose a file message and save it
  react       Add or remove a reaction
  unsend      Retract one of your own messages
  version     Show the installed version
  help        Show this help

Examples:
  line messages "Alice"
  line send "Alice" --text "Hello!"
  line send "Alice" --file report.pdf
  line chats --search "Family"

In a terminal, missing details are prompted for. Ctrl-C cancels.
Use --show-ids for IDs, --json for scripts, or COMMAND --help for all options.
`

type App struct {
	Interactive      bool
	input            *bufio.Reader
	promptAccount    *promptAccount
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
	NewHeadlessLogin func() (session.LoginStorage, error)
}

// Run validates command arguments before accessing Keychain or the network.
func (a *App) Run(args []string) error {
	a.input, a.promptAccount = nil, nil
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
	case "auth":
		return a.authCommand(args[1:])
	case "download":
		return a.downloadCommand(args[1:])
	case "react", "unsend":
		return a.actionCommand(command, args[1:])
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
	var headless bool
	var showIDs bool
	var search string
	limit := 20
	if command == "contacts" {
		fs.StringVar(&search, "search", "", "find contacts by name")
		fs.IntVar(&limit, "limit", 20, "maximum rows; 0 shows all")
	}
	if command == "contacts" || command == "whoami" {
		fs.BoolVar(&showIDs, "show-ids", false, "show full IDs")
	}
	if command == "login" {
		fs.StringVar(&email, "email", "", "LINE account email (prompted in a terminal)")
		fs.BoolVar(&headless, "headless", false, "Linux: explicitly enroll headless host-key storage; preserve existing headless protection")
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
	if limit < 0 {
		return errors.New("limit must be 0 or greater")
	}
	if command == "login" {
		if headless {
			return a.headlessLogin(strings.TrimSpace(email))
		}
		if a.Interactive && strings.TrimSpace(email) == "" {
			keep, err := a.keepExistingLogin()
			if err != nil {
				return err
			}
			if keep {
				_, err := fmt.Fprintln(a.Out, "Kept your current session. Next: line chats")
				return err
			}
		}
		if strings.TrimSpace(email) == "" {
			if !a.Interactive {
				return errors.New("provide --email ADDRESS; run line login in a terminal for guided sign-in")
			}
			var err error
			email, err = a.ask("Email: ")
			if err != nil {
				return err
			}
			if strings.TrimSpace(email) == "" {
				return errors.New("email cannot be empty")
			}
		}
		return a.login(strings.TrimSpace(email))
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
		_, err = fmt.Fprintf(a.Out, "Signed in as %s\nSession saved securely on this device.\n", terminalText(profile.DisplayName))
		if err == nil && showIDs {
			_, err = fmt.Fprintf(a.Out, "ID: %s\n", terminalText(profile.Mid))
		}
		return err
	case "contacts":
		contacts, err := a.contacts()
		if err != nil {
			return err
		}
		filtered := make([]line.Contact, 0, len(contacts))
		for _, c := range contacts {
			if strings.Contains(strings.ToLower(c.EffectiveDisplayName()), strings.ToLower(search)) {
				filtered = append(filtered, c)
			}
		}
		limitSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" {
				limitSet = true
			}
		})
		if jsonOutput && !limitSet {
			limit = 0
		}
		total := len(filtered)
		if limit > 0 {
			filtered = filtered[:min(limit, total)]
		}
		if jsonOutput {
			return a.json(filtered)
		}
		if total == 0 {
			_, err = fmt.Fprintln(a.Out, "No contacts found. Try a different --search.")
			return err
		}
		fmt.Fprintln(a.Out, "CONTACT")
		for _, c := range filtered {
			name := c.EffectiveDisplayName()
			if name == "" {
				name = c.Mid
			}
			fmt.Fprintln(a.Out, terminalText(name))
			if showIDs && name != c.Mid {
				fmt.Fprintln(a.Out, "  "+terminalText(c.Mid))
			}
		}
		_, err = fmt.Fprintf(a.Out, "\nShowing %d of %d contacts. Find someone: line contacts --search NAME\n", len(filtered), total)
		return err
	}
	return nil
}

func (a *App) login(email string) error {
	var before *loginStorageSnapshot
	if a.Manager.Storage != nil {
		unlock, err := a.lock()
		if err != nil {
			return err
		}
		err = a.Manager.PrepareStorage()
		if err == nil {
			var snapshot loginStorageSnapshot
			snapshot, err = snapshotLoginStorage(a.Manager.Store)
			before = &snapshot
		}
		unlock()
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(a.Err, "Signing in may replace your existing LINE Chrome-style session.")
	password, err := a.Password()
	if err != nil {
		return err
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if before != nil {
		after, err := snapshotLoginStorage(a.Manager.Store)
		if err != nil {
			return err
		}
		if after != *before {
			return errors.New("your saved session changed during password input; run login again")
		}
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
	_, err = fmt.Fprintf(a.Out, "Signed in as %s. Session saved securely.\nNext: line chats\n", terminalText(profile.DisplayName))
	return err
}

type loginStorageSnapshot struct {
	exists          bool
	mid, generation string
	invalidated     bool
	storageIdentity string
}

func snapshotLoginStorage(store session.Store) (loginStorageSnapshot, error) {
	var identity string
	if selector, ok := store.(interface{ StorageIdentity() (string, error) }); ok {
		var err error
		identity, err = selector.StorageIdentity()
		if err != nil {
			return loginStorageSnapshot{}, err
		}
	}
	s, err := store.Load()
	if errors.Is(err, session.ErrNotFound) {
		return loginStorageSnapshot{storageIdentity: identity}, nil
	}
	if err != nil {
		return loginStorageSnapshot{}, err
	}
	if s == nil {
		return loginStorageSnapshot{}, errors.New("saved session is invalid")
	}
	return loginStorageSnapshot{true, s.MID, s.Generation, s.Invalidated, identity}, nil
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
