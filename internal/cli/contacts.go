package cli

import (
	"errors"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func (a *App) contactsCommand(args []string) error {
	fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line contacts [options]\n       line contacts --mid MID [--mid MID ...] [--json]\nLists friends, or looks up supplied user IDs including non-friends.\nDirect lookup shows all requested IDs and names; unavailable contacts produce exit status 1.")
		fs.PrintDefaults()
	}
	var jsonOutput, showIDs bool
	var search string
	var mids []string
	limit := 20
	fs.StringVar(&search, "search", "", "find friends by name (cannot combine with --mid)")
	fs.IntVar(&limit, "limit", 20, "maximum friend-list rows; 0 shows all (cannot combine with --mid)")
	fs.Func("mid", "look up a user MID directly; repeat for multiple IDs", func(mid string) error {
		mids = append(mids, mid)
		return nil
	})
	fs.BoolVar(&showIDs, "show-ids", false, "show full IDs")
	fs.BoolVar(&jsonOutput, "json", false, "write JSON to stdout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line contacts --help")
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; use --mid MID for each user ID or --search NAME to find friends")
	}
	limitSet, searchSet := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "limit":
			limitSet = true
		case "search":
			searchSet = true
		}
	})
	if len(mids) > 0 {
		if limitSet || searchSet {
			return errors.New("--mid cannot be combined with --search or --limit; direct lookup returns every requested user ID")
		}
		seen := make(map[string]bool)
		unique := make([]string, 0, len(mids))
		for i, mid := range mids {
			if !fullUserMID.MatchString(mid) {
				return fmt.Errorf("invalid user MID at --mid #%d; provide a full user ID (u/U), not a group/room ID or display name", i+1)
			}
			if !seen[mid] {
				unique = append(unique, mid)
				seen[mid] = true
			}
		}
		mids = unique
	}
	if limit < 0 {
		return errors.New("limit must be 0 or greater")
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if len(mids) > 0 {
		return a.lookupContacts(mids, jsonOutput)
	}
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

func (a *App) contacts() ([]line.Contact, error) {
	var ids []string
	if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) { ids, err = api.GetAllContactIdsContext(a.loginContext()); return }); err != nil {
		return nil, err
	}
	contacts, err := a.fetchContacts(ids)
	if err != nil {
		return nil, err
	}
	result := make([]line.Contact, 0, len(contacts))
	for _, contact := range contacts {
		result = append(result, contact)
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

// Fetch all batches before rendering so a failed request cannot publish partial
// results. A missing map entry is unavailable; an inconsistent response is fatal.
func (a *App) fetchContacts(ids []string) (map[string]line.Contact, error) {
	result := make(map[string]line.Contact, len(ids))
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		var response *line.ContactsResponse
		if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetContactsV2Context(a.loginContext(), batch)
			return
		}); err != nil {
			return nil, err
		}
		if response == nil || response.Contacts == nil {
			return nil, errors.New("LINE returned an invalid contacts response")
		}
		requested := make(map[string]bool, len(batch))
		for _, mid := range batch {
			requested[mid] = true
		}
		for mid, wrapper := range response.Contacts {
			contact := wrapper.Contact
			if !requested[mid] || (contact.Mid != "" && contact.Mid != mid) {
				return nil, errors.New("LINE returned inconsistent contact IDs")
			}
			if contact.Mid == "" {
				contact.Mid = mid
			}
			result[mid] = contact
		}
	}
	return result, nil
}

// Match the supported user-ID forms in fullChatID, preserving case because
// current opaque MIDs contain case-sensitive base64url characters.
var fullUserMID = regexp.MustCompile(`^(?:[uU][A-Za-z0-9_-]{43}|u[0-9a-f]{32})$`)

type contactLookupResult struct {
	line.Contact
	EffectiveName string `json:"effectiveDisplayName"`
	Status        string `json:"status"`
}

func (a *App) lookupContacts(mids []string, jsonOutput bool) error {
	contacts, err := a.fetchContacts(mids)
	if err != nil {
		return err
	}
	results := make([]contactLookupResult, 0, len(mids))
	unavailable := 0
	for _, mid := range mids {
		contact, found := contacts[mid]
		result := contactLookupResult{Contact: contact, EffectiveName: contact.EffectiveDisplayName(), Status: "resolved"}
		if !found {
			result.Mid = mid
			result.Status = "unavailable"
			unavailable++
		}
		results = append(results, result)
	}
	if jsonOutput {
		if err := a.json(results); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(a.Out, "USER ID  CONTACT"); err != nil {
			return err
		}
		for _, result := range results {
			name := terminalText(result.EffectiveName)
			if result.Status == "unavailable" {
				name = "(unavailable)"
			} else if strings.TrimSpace(name) == "" {
				name = "(name unavailable)"
			}
			if _, err := fmt.Fprintf(a.Out, "%s  %s\n", result.Mid, name); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(a.Out, "\nResolved %d of %d contacts.\n", len(results)-unavailable, len(results)); err != nil {
			return err
		}
	}
	if unavailable > 0 {
		return fmt.Errorf("%d of %d requested contacts unavailable; LINE did not return their profiles", unavailable, len(results))
	}
	return nil
}
