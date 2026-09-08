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

func (a *App) chatCommand(args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line chats [options]\nShows 20 active conversations, newest first. Search also includes inactive chats.")
		fs.PrintDefaults()
	}
	all := fs.Bool("all", false, "include inactive conversations")
	search := fs.String("search", "", "find chat names containing this text (case-insensitive)")
	limit := fs.Int("limit", 20, "maximum rows; 0 shows all")
	ids := fs.Bool("show-ids", false, "show full IDs below names")
	jsonOutput := fs.Bool("json", false, "write JSON (all chats, unlimited by default)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line chats --help")
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; use line chats --search NAME")
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
	unlock, err := a.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	// Preserve the original unbounded ID-only JSON contract for scripts.
	legacyJSON := *jsonOutput && *search == ""
	chats, err := a.chatBoxes(!*all && !*jsonOutput && *search == "", !legacyJSON)
	if err != nil {
		return err
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
		return err
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
	if *jsonOutput {
		return a.json(chats)
	}
	if len(chats) == 0 {
		_, err := fmt.Fprintln(a.Out, "No conversations found. Try line chats --all or a different --search.")
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
	_, err = fmt.Fprintf(a.Out, "\nShowing %d of %d conversations. Use --limit 0 for all rows, --search NAME to find a chat.\nRead: line messages \"Chat name\"   •   IDs: line chats --show-ids\n", len(chats), total)
	return err
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
		if err := a.Manager.Do(func(api session.API) (err error) {
			response, err = api.GetContactsV2(direct[start:min(start+100, len(direct))])
			return
		}); err != nil {
			return err
		}
		if response == nil {
			return errors.New("LINE returned no contact names")
		}
		for id, wrapper := range response.Contacts {
			names[id] = wrapper.Contact.EffectiveDisplayName()
		}
	}
	// Use the validated LINE group lookup batch size.
	for start := 0; start < len(groups); start += 20 {
		var response *line.GetChatsResponse
		if err := a.Manager.Do(func(api session.API) (err error) {
			response, err = api.GetChats(groups[start:min(start+20, len(groups))], false, false)
			return
		}); err != nil {
			return err
		}
		if response == nil {
			return errors.New("LINE returned no group names")
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
	for _, chat := range chats {
		if strings.EqualFold(chat.Name, selector) {
			matches = append(matches, chat.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", errors.New("no chat has that exact name; use line chats --search NAME --show-ids, then copy the full name or ID")
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%d chats have that name; use line chats --search NAME --show-ids and choose a full ID", len(matches))
	}
}
