package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

var ErrCancelled = errors.New("cancelled")

type promptAccount struct{ mid, generation string }

func (a *App) keepExistingLogin() (bool, error) {
	unlock, err := a.Lock()
	if err != nil {
		return false, err
	}
	s, err := a.Manager.Store.Load()
	if errors.Is(err, session.ErrNotFound) || (err == nil && (s == nil || s.Invalidated)) {
		unlock()
		return false, nil
	}
	if err != nil {
		unlock()
		return false, err
	}
	name := "your saved account"
	var profile *line.Profile
	err = a.Manager.Do(func(api session.API) (err error) { profile, err = api.GetProfile(); return })
	if err != nil {
		unlock()
		return false, nil
	}
	if profile != nil && profile.DisplayName != "" {
		name = profile.DisplayName
	}
	err = a.rememberAccount()
	unlock()
	if err != nil {
		return false, err
	}
	fmt.Fprintln(a.Err, "Already signed in as "+terminalText(name)+".")
	answer, err := a.ask("Sign in again? [y/N]: ")
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(strings.TrimSpace(answer), "y"), nil
}

// Prompts never hold a credential lock. Recheck account identity when work resumes.
func (a *App) lock() (func(), error) {
	unlock, err := a.Lock()
	if err != nil {
		return nil, err
	}
	if a.promptAccount != nil {
		s, err := a.Manager.Store.Load()
		if err != nil || s == nil || s.Invalidated || s.MID != a.promptAccount.mid || s.Generation != a.promptAccount.generation {
			unlock()
			return nil, errors.New("your account changed while choosing; run the command again")
		}
	}
	return unlock, nil
}

func (a *App) rememberAccount() error {
	s, err := a.Manager.Store.Load()
	if err != nil {
		return err
	}
	if s == nil || s.AccessToken == "" || s.Invalidated {
		return session.ErrNotFound
	}
	a.promptAccount = &promptAccount{s.MID, s.Generation}
	return nil
}

func (a *App) ask(label string) (string, error) {
	if !a.Interactive || a.In == nil {
		return "", errors.New("interactive input unavailable; provide command options")
	}
	if a.Context != nil {
		if err := a.Context.Err(); err != nil {
			return "", ErrCancelled
		}
	}
	if _, err := fmt.Fprint(a.Err, label); err != nil {
		return "", err
	}
	if a.input == nil {
		a.input = bufio.NewReader(a.In)
	}
	var answer strings.Builder
	for {
		part, err := a.input.ReadSlice('\n')
		if answer.Len()+len(part) > messaging.MaxTextUnits*4+1 {
			return "", errors.New("input exceeds the CLI size limit")
		}
		answer.Write(part)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			return "", ErrCancelled
		}
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(answer.String(), "\n"), "\r"), nil
	}
}

type choice struct{ id, label string }

func (a *App) choose(title string, options []choice, search bool) (string, error) {
	query, page := "", 0
	for {
		filtered := make([]choice, 0, len(options))
		for _, item := range options {
			if strings.Contains(strings.ToLower(item.label), strings.ToLower(query)) {
				filtered = append(filtered, item)
			}
		}
		fmt.Fprintln(a.Err, "\n"+title)
		start := page * 20
		end := min(start+20, len(filtered))
		if len(filtered) == 0 {
			fmt.Fprintln(a.Err, "No matches. Try another search.")
		}
		for i := start; i < end; i++ {
			fmt.Fprintf(a.Err, "  %d  %s\n", i-start+1, terminalText(filtered[i].label))
		}
		label := "Choose a number (n: next, p: previous, q: cancel): "
		if search {
			label = "Number or /search (n: next, p: previous, q: cancel): "
		}
		answer, err := a.ask(label)
		if err != nil {
			return "", err
		}
		answer = strings.TrimSpace(answer)
		switch answer {
		case "q":
			return "", ErrCancelled
		case "n":
			if end < len(filtered) {
				page++
			}
			continue
		case "p":
			if page > 0 {
				page--
			}
			continue
		case "":
			continue
		}
		if n, err := strconv.Atoi(answer); err == nil && n > 0 && n <= end-start {
			return filtered[start+n-1].id, nil
		}
		if search {
			query, page = strings.TrimPrefix(answer, "/"), 0
		} else {
			fmt.Fprintln(a.Err, "Choose one of the displayed numbers.")
		}
	}
}

func (a *App) selectChat(selector string) (string, string, error) {
	unlock, err := a.lock()
	if err != nil {
		return "", "", err
	}
	if err := a.rememberAccount(); err != nil {
		unlock()
		return "", "", err
	}
	if selector != "" {
		id, err := a.resolveChat(selector)
		unlock()
		return "id:" + id, selector, err
	}
	chats, err := a.chatBoxes(false, true)
	if err == nil {
		err = a.nameChats(chats)
	}
	if err == nil {
		// Contacts allow a first message without an existing conversation.
		var contacts []choice
		list, contactErr := a.contacts()
		if contactErr != nil {
			err = contactErr
		} else {
			seen := make(map[string]bool)
			for _, c := range chats {
				seen[c.ID] = true
			}
			for _, c := range list {
				if !seen[c.Mid] {
					contacts = append(contacts, choice{c.Mid, c.EffectiveDisplayName()})
				}
			}
			for _, c := range contacts {
				chats = append(chats, Chat{ID: c.id, Name: c.label})
			}
		}
	}
	unlock()
	if err != nil {
		return "", "", err
	}
	if len(chats) == 0 {
		return "", "", errors.New("no chats or contacts found")
	}
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].UpdatedAt > chats[j].UpdatedAt })
	counts := make(map[string]int)
	for _, c := range chats {
		counts[strings.ToLower(c.Name)]++
	}
	options := make([]choice, 0, len(chats))
	for _, c := range chats {
		name := c.Name
		if name == "" {
			name = c.ID
		}
		label := name + " · " + chatKind(c.ID)
		if counts[strings.ToLower(c.Name)] > 1 {
			label += " · " + c.ID
		}
		if c.UnreadCount != "" && c.UnreadCount != "0" {
			label += " · " + c.UnreadCount.String() + " unread"
		}
		options = append(options, choice{c.ID, label})
	}
	id, err := a.choose("Choose a chat", options, true)
	if err != nil {
		return "", "", err
	}
	for _, c := range chats {
		if c.ID == id {
			name := c.Name
			if name == "" {
				name = id
			}
			return "id:" + id, name, nil
		}
	}
	return "", "", ErrCancelled
}

func (a *App) selectMessage(chat, command string) (string, error) {
	unlock, err := a.lock()
	if err != nil {
		return "", err
	}
	if err = a.rememberAccount(); err != nil {
		unlock()
		return "", err
	}
	id, err := a.resolveChat(chat)
	var messages []messaging.Message
	if err == nil {
		var c *messaging.Client
		c, err = messaging.New(a.Manager)
		if err == nil {
			messages, err = c.History(id, 100)
		}
	}
	unlock()
	if err != nil {
		return "", err
	}
	options := make([]choice, 0, len(messages))
	for _, m := range messages {
		if command == "unsend" && m.From != a.promptAccount.mid {
			continue
		}
		if command == "download" && m.ContentType != 14 {
			continue
		}
		body := messagePreview(m)
		if r := []rune(body); len(r) > 90 {
			body = string(r[:90]) + "…"
		}
		options = append(options, choice{m.ID, body + " · ID " + m.ID})
	}
	if len(options) == 0 {
		return "", errors.New("no eligible messages among the latest 100")
	}
	return a.choose("Choose a message to "+command, options, true)
}
