package cli

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

var errNoNames = errors.New("LINE returned no names")

func (a *App) chatCommand(args []string) error {
	fs := newFlagSet("chats")
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	h := commandHelp{Command: "chats", Usage: []string{"line chats [options]"},
		About:    "Show 20 active conversations, newest first. Search also includes inactive\nchats. JSON output includes every chat unless you set a limit.",
		Groups:   []flagGroup{{"Filter", []string{"search", "unread", "all"}}, {"Output", []string{"limit", "show-ids", "details", "json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{"line chats", "line chats --unread", `line chats --search "Family" --show-ids`}}
	all := fs.Bool("all", false, "include inactive conversations")
	search := fs.String("search", "", "find chat names containing this text (case-insensitive)")
	limit := fs.Int("limit", 20, "maximum rows; 0 shows all")
	ids := fs.Bool("show-ids", false, "show full IDs below names")
	unread := fs.Bool("unread", false, "only chats with unread messages (reported count above zero; unknown counts excluded)")
	details := fs.Bool("details", false, "include names, activity, and unread counts in a versioned envelope (requires --json)")
	jsonOutput := fs.Bool("json", false, "write JSON (all chats, unlimited by default)")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; use line chats --search NAME")
	}
	if *details && !*jsonOutput {
		return errors.New("--details requires --json")
	}
	if *limit < 0 {
		return errors.New("limit must be 0 or greater")
	}
	limitSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			limitSet = true
		}
	})
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	// Preserve the original unbounded ID-only JSON contract for scripts.
	legacyJSON := *jsonOutput && *search == "" && !*details
	chats, err := a.chatBoxes(!*all && !*jsonOutput && *search == "", !legacyJSON)
	if err != nil {
		return err
	}
	// Filter on the reported counts; the server's unreadOnly option is unverified.
	if *unread {
		chats = unreadChats(chats)
	}
	if legacyJSON {
		if limitSet && *limit > 0 {
			chats = chats[:min(*limit, len(chats))]
		}
		return a.json(chats)
	}
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].UpdatedAt > chats[j].UpdatedAt })
	if *jsonOutput && !limitSet {
		*limit = 0
	}
	total := len(chats)
	if *search == "" && *limit > 0 {
		chats = chats[:min(*limit, len(chats))]
	}
	if err := a.nameChats(chats); err != nil {
		if !*details || *search != "" {
			return err
		}
		if fatal := enrichmentError(err); fatal != nil {
			return fatal
		}
	}
	if *search != "" {
		matches := make([]Chat, 0)
		for _, chat := range chats {
			if strings.Contains(strings.ToLower(chat.Name), strings.ToLower(*search)) {
				matches = append(matches, chat)
			}
		}
		chats, total = matches, len(matches)
		if *limit > 0 {
			chats = chats[:min(*limit, len(chats))]
		}
	}
	if *details {
		return a.writeDetailedChats(chats)
	}
	if *jsonOutput {
		return a.json(chats)
	}
	if len(chats) == 0 {
		message := "No conversations found. Try line chats --all or a different --search."
		if *unread {
			message = "No unread conversations found. Try line chats --unread --all to include inactive chats."
		}
		_, err := fmt.Fprintln(a.Out, message)
		return err
	}
	if _, err := fmt.Fprintf(a.Out, "%-16s  %6s  %-7s  %s\n", "UPDATED", "UNREAD", "TYPE", "CHAT"); err != nil {
		return err
	}
	for _, chat := range chats {
		name := terminalText(chat.Name)
		if strings.TrimSpace(name) == "" {
			name = "(name unavailable)"
		}
		updated := "—"
		if chat.UpdatedAt > 0 {
			updated = time.UnixMilli(chat.UpdatedAt).Local().Format("2006-01-02 15:04")
		}
		unread := terminalText(chat.UnreadCount.String())
		if unread == "0" {
			unread = "—"
		}
		if _, err := fmt.Fprintf(a.Out, "%-16s  %6s  %-7s  %s\n", updated, unread, chatKind(chat.ID), name); err != nil {
			return err
		}
		if *ids || strings.TrimSpace(chat.Name) == "" {
			if _, err := fmt.Fprintf(a.Out, "                                    %s\n", terminalText(chat.ID)); err != nil {
				return err
			}
		}
	}
	kind := "conversations"
	if *unread {
		kind = "unread conversations"
	}
	_, err = fmt.Fprintf(a.Out, "\nShowing %d of %d %s. Use --limit 0 for all rows, --search NAME to find a chat.\nRead: line messages \"Chat name\"   •   IDs: line chats --show-ids\n", len(chats), total, kind)
	return err
}

// unreadChats keeps chats with a known unread count above zero. A missing or
// malformed count is not evidence of unread messages, so it is excluded.
func unreadChats(chats []Chat) []Chat {
	kept := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if n, err := chat.UnreadCount.Int64(); chat.UnreadKnown && err == nil && n > 0 {
			kept = append(kept, chat)
		}
	}
	return kept
}

func chatKind(id string) string {
	if id == "" {
		return "Unknown"
	}
	switch strings.ToLower(id[:1]) {
	case "u":
		return "Direct"
	case "c":
		return "Group"
	case "r":
		return "Room"
	default:
		return "Unknown"
	}
}

// Resolve titles in batches without fetching, decoding, or storing message text.
// Request failures are fatal rather than selecting from partially fetched pages.
func (a *App) nameChats(chats []Chat) error {
	var direct, groups []string
	for _, chat := range chats {
		if chatKind(chat.ID) == "Direct" {
			direct = append(direct, chat.ID)
		} else {
			groups = append(groups, chat.ID)
		}
	}
	names := make(map[string]string)
	for start := 0; start < len(direct); start += 100 {
		var response *line.ContactsResponse
		if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetContactsV2Context(a.loginContext(), direct[start:min(start+100, len(direct))])
			return
		}); err != nil {
			return err
		}
		if response == nil {
			return errNoNames
		}
		for id, wrapper := range response.Contacts {
			names[id] = wrapper.Contact.EffectiveDisplayName()
		}
	}
	// Use the validated LINE group lookup batch size.
	for start := 0; start < len(groups); start += 20 {
		var response *line.GetChatsResponse
		if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetChatsContext(a.loginContext(), groups[start:min(start+20, len(groups))], false, false)
			return
		}); err != nil {
			return err
		}
		if response == nil {
			return errNoNames
		}
		for _, chat := range response.Chats {
			names[chat.ChatMid] = chat.ChatName
		}
	}
	for i := range chats {
		chats[i].Name = names[chats[i].ID]
	}
	return nil
}

// Current opaque IDs are a prefix plus 43 base64url characters; legacy MIDs
// contain 32 hex digits. Short names such as Charlie must not be treated as IDs.
var fullChatID = regexp.MustCompile(`^(?:[uUcCrR][A-Za-z0-9_-]{43}|[ucr][0-9a-f]{32})$`)

func validateSelector(selector string) error {
	if strings.TrimSpace(selector) == "" || !utf8.ValidString(selector) || strings.ContainsFunc(selector, unicode.IsControl) {
		return errors.New("provide a chat ID or exact name; run line chats to find one")
	}
	if strings.HasPrefix(selector, "id:") {
		return messaging.ValidateChatID(strings.TrimPrefix(selector, "id:"))
	}
	return nil
}

func (a *App) resolveChat(selector string) (string, error) {
	if strings.HasPrefix(selector, "id:") {
		return strings.TrimPrefix(selector, "id:"), nil
	}
	if fullChatID.MatchString(selector) {
		return selector, nil
	}
	chats, err := a.chatBoxes(false, false)
	if err != nil {
		return "", err
	}
	if err := a.nameChats(chats); err != nil {
		return "", err
	}
	var matches []string
	seen := make(map[string]bool)
	for _, chat := range chats {
		if strings.EqualFold(chat.Name, selector) {
			matches = append(matches, chat.ID)
			seen[chat.ID] = true
		}
	}
	contacts, err := a.contacts()
	if err != nil {
		return "", err
	}
	for _, contact := range contacts {
		if strings.EqualFold(contact.EffectiveDisplayName(), selector) && !seen[contact.Mid] {
			matches = append(matches, contact.Mid)
			seen[contact.Mid] = true
		}
	}
	switch len(matches) {
	case 0:
		return "", errors.New("no chat or contact has that exact name; use line chats --search NAME or line contacts --search NAME --show-ids")
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%d chats or contacts have that name; use --show-ids with line chats or line contacts and choose a full ID", len(matches))
	}
}
