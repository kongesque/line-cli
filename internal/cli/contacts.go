package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

func (a *App) contactsCommand(args []string) error {
	if len(args) > 0 && (args[0] == "block" || args[0] == "unblock") {
		return a.contactsBlockCommand(args[0], args[1:])
	}
	fs := newFlagSet("contacts")
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	h := commandHelp{
		Command: "contacts",
		Usage: []string{
			"line contacts [options]",
			"line contacts --mid MID [--mid MID ...] [--json]",
			"line contacts --userid HANDLE [--json]",
			"line contacts --phone +CCNUMBER [--json]",
			"line contacts --blocked [--show-ids] [--json]",
			"line contacts block USER_MID [--json]",
			"line contacts unblock USER_MID [--json]",
		},
		About: "List friends, look up users by MID, LINE ID, or phone number (including\npeople who aren't friends), or list blocked contacts. Direct MID lookup shows\nevery requested ID; unavailable profiles exit with status 1.\nUse contacts block/unblock to manage blocks.",
		Groups: []flagGroup{
			{"Find", []string{"search", "mid", "userid", "phone", "blocked"}},
			{"Output", []string{"limit", "show-ids", "json"}},
			{"Request", []string{"timeout"}},
		},
		Examples: []string{
			`line contacts --search "Alice"`,
			"line contacts --mid USER_MID --json",
			"line contacts --userid @alice --json",
			`line contacts --phone "+81 9012345678" --json`,
			"line contacts --blocked --show-ids",
			"line contacts block USER_MID",
		},
	}
	var jsonOutput, showIDs, blocked bool
	var search, userid, phone string
	var mids []string
	limit := 20
	fs.StringVar(&search, "search", "", "find friends by name (cannot combine with --mid, --userid, --phone, or --blocked)")
	fs.IntVar(&limit, "limit", 20, "maximum friend-list rows; 0 shows all (cannot combine with --mid, --userid, --phone, or --blocked)")
	fs.Func("mid", "look up a user MID directly; repeat for multiple IDs", func(mid string) error {
		mids = append(mids, mid)
		return nil
	})
	fs.StringVar(&userid, "userid", "", "look up a user by LINE ID (leading @ optional)")
	fs.StringVar(&phone, "phone", "", "look up a user by international phone number, e.g. +819012345678 or '+81 9012345678'")
	fs.BoolVar(&blocked, "blocked", false, "list blocked contacts and blocked recommendations")
	fs.BoolVar(&showIDs, "show-ids", false, "show full IDs")
	fs.BoolVar(&jsonOutput, "json", false, "write JSON to stdout")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; use --mid, --userid, --phone, --search, --blocked, or contacts block|unblock USER_MID")
	}
	limitSet, searchSet, useridSet, phoneSet := false, false, false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "limit":
			limitSet = true
		case "search":
			searchSet = true
		case "userid":
			useridSet = true
		case "phone":
			phoneSet = true
		}
	})
	specialModes := 0
	if len(mids) > 0 {
		specialModes++
	}
	if useridSet {
		specialModes++
	}
	if phoneSet {
		specialModes++
	}
	if blocked {
		specialModes++
	}
	if specialModes > 1 {
		return errors.New("use only one of --mid, --userid, --phone, or --blocked")
	}
	if specialModes > 0 && (limitSet || searchSet) {
		return errors.New("--mid, --userid, --phone, and --blocked cannot be combined with --search or --limit")
	}
	if len(mids) > 0 {
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
	if useridSet {
		userid, err = normalizeUserID(userid)
		if err != nil {
			return err
		}
	}
	if phoneSet {
		phone, err = formatLinePhone(phone)
		if err != nil {
			return err
		}
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
	if useridSet {
		return a.findContactByUserID(userid, jsonOutput)
	}
	if phoneSet {
		return a.findContactByPhone(phone, jsonOutput)
	}
	if blocked {
		return a.listBlockedContacts(showIDs, jsonOutput)
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

func (a *App) contactsBlockCommand(sub string, args []string) error {
	var target string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
	}
	name := "contacts " + sub
	fs := newFlagSet(name)
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	jsonOutput := fs.Bool("json", false, "write JSON to stdout")
	about := "Block a LINE user by full user ID (u/U). Changes take effect on LINE's servers."
	if sub == "unblock" {
		about = "Unblock a LINE user by full user ID (u/U). Changes take effect on LINE's servers."
	}
	h := commandHelp{
		Command:  name,
		Usage:    []string{fmt.Sprintf("line contacts %s USER_MID [options]", sub)},
		About:    about,
		Groups:   []flagGroup{{"Output", []string{"json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{fmt.Sprintf("line contacts %s USER_MID", sub), fmt.Sprintf("line contacts %s USER_MID --json", sub)},
	}
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if err := a.refuseReadOnly(sub + "ing a contact"); err != nil {
		return err
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	switch {
	case target == "" && fs.NArg() == 1:
		target = fs.Arg(0)
	case fs.NArg() != 0:
		return errors.New("unexpected arguments; place options after the user ID")
	}
	if err := messaging.ValidateUserMID(target); err != nil {
		return err
	}
	if a.errorMutation == nil {
		a.errorMutation = &fault.Mutation{Outcome: fault.NotAttempted}
	}
	a.errorMutation.Action, a.errorMutation.ChatID = sub+"_contact", target
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	change := messaging.BlockContactContext
	if sub == "unblock" {
		change = messaging.UnblockContactContext
	}
	result, err := change(a.loginContext(), a.Manager, target)
	if err != nil {
		return err
	}
	op := &fault.Mutation{
		Action:          result.Action,
		ChatID:          result.ChatID,
		RequestSequence: &result.RequestSequence,
		Attempted:       true,
		Outcome:         fault.Confirmed,
	}
	if *jsonOutput {
		return a.confirmedOutput(op, a.json(result))
	}
	verb := "Blocked"
	if sub == "unblock" {
		verb = "Unblocked"
	}
	_, err = fmt.Fprintf(a.Out, "%s %s.\n", verb, terminalText(result.ChatID))
	return a.confirmedOutput(op, err)
}

var validLineUserID = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

func normalizeUserID(raw string) (string, error) {
	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if !validLineUserID.MatchString(handle) {
		return "", errors.New("invalid LINE ID; use letters, digits, '.', '_', or '-' (leading @ optional)")
	}
	return handle, nil
}

// ITU-T E.164 2-digit country calling codes (1-digit codes are 1 and 7; all
// other valid country calling codes are 3 digits).
var twoDigitCountryCodes = map[string]bool{
	"20": true, "27": true,
	"30": true, "31": true, "32": true, "33": true, "34": true, "36": true, "39": true,
	"40": true, "41": true, "43": true, "44": true, "45": true, "46": true, "47": true, "48": true, "49": true,
	"51": true, "52": true, "53": true, "54": true, "55": true, "56": true, "57": true, "58": true,
	"60": true, "61": true, "62": true, "63": true, "64": true, "65": true, "66": true,
	"81": true, "82": true, "84": true, "86": true,
	"90": true, "91": true, "92": true, "93": true, "94": true, "95": true, "98": true,
}

// formatLinePhone validates an international phone number and formats it as
// "+<countryCode> <nationalNumber>" for TalkService/findContactsByPhone.
func formatLinePhone(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "+") {
		return "", errors.New("invalid phone number; use international format starting with +, e.g. +819012345678 or '+81 9012345678'")
	}
	rest := s[1:]
	if idx := strings.IndexAny(rest, " \t"); idx >= 0 {
		cc := rest[:idx]
		natRaw := strings.TrimSpace(rest[idx+1:])
		nat, ok := stripPhoneSeparators(natRaw)
		if !ok || !allDigits(cc) || len(cc) < 1 || len(cc) > 3 || cc[0] == '0' || len(nat) < 4 || len(cc)+len(nat) > 15 {
			return "", errors.New("invalid phone number; use international format starting with +, e.g. +819012345678 or '+81 9012345678'")
		}
		return "+" + cc + " " + nat, nil
	}
	digits, ok := stripPhoneSeparators(rest)
	if !ok || len(digits) < 7 || len(digits) > 15 || digits[0] == '0' {
		return "", errors.New("invalid phone number; use international format starting with +, e.g. +819012345678 or '+81 9012345678'")
	}
	ccLen := 3
	if digits[0] == '1' || digits[0] == '7' {
		ccLen = 1
	} else if twoDigitCountryCodes[digits[:2]] {
		ccLen = 2
	}
	if len(digits)-ccLen < 4 {
		return "", errors.New("invalid phone number; use international format starting with +, e.g. +819012345678 or '+81 9012345678'")
	}
	return "+" + digits[:ccLen] + " " + digits[ccLen:], nil
}

func stripPhoneSeparators(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '-' || c == ' ' || c == '(' || c == ')' || c == '.':
		default:
			return "", false
		}
	}
	return b.String(), b.Len() > 0
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (a *App) findContactByUserID(userid string, jsonOutput bool) error {
	var contact *line.Contact
	err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
		contact, err = api.FindContactByUseridContext(a.loginContext(), userid)
		return
	})
	if err != nil {
		if line.IsContactNotFound(session.ProtocolError(err)) {
			return fault.New("not_found", "lookup", "No LINE user was found for that LINE ID.", err)
		}
		return err
	}
	if contact == nil || contact.Mid == "" {
		return fault.New("not_found", "lookup", "No LINE user was found for that LINE ID.", nil)
	}
	return a.renderSingleContact(*contact, jsonOutput)
}

func (a *App) findContactByPhone(phone string, jsonOutput bool) error {
	var contact *line.Contact
	err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
		contact, err = api.FindContactByPhoneContext(a.loginContext(), phone)
		return
	})
	if err != nil {
		if line.IsContactNotFound(session.ProtocolError(err)) {
			return fault.New("not_found", "lookup", "No LINE user was found for that phone number.", err)
		}
		return err
	}
	if contact == nil || contact.Mid == "" {
		return fault.New("not_found", "lookup", "No LINE user was found for that phone number.", nil)
	}
	return a.renderSingleContact(*contact, jsonOutput)
}

func (a *App) renderSingleContact(contact line.Contact, jsonOutput bool) error {
	result := contactLookupResult{
		Contact:       contact,
		EffectiveName: contact.EffectiveDisplayName(),
		Status:        "resolved",
	}
	if jsonOutput {
		return a.json(result)
	}
	name := terminalText(result.EffectiveName)
	if strings.TrimSpace(name) == "" {
		name = "(name unavailable)"
	}
	if _, err := fmt.Fprintln(a.Out, "USER ID  CONTACT"); err != nil {
		return err
	}
	_, err := fmt.Fprintf(a.Out, "%s  %s\n", terminalText(result.Mid), name)
	return err
}

func (a *App) listBlockedContacts(showIDs, jsonOutput bool) error {
	var blockedIDs, recIDs []string
	if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
		blockedIDs, err = api.GetBlockedContactIdsContext(a.loginContext())
		return
	}); err != nil {
		return err
	}
	if err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
		recIDs, err = api.GetBlockedRecommendationIdsContext(a.loginContext())
		return
	}); err != nil {
		return err
	}
	seen := make(map[string]bool, len(blockedIDs)+len(recIDs))
	ids := make([]string, 0, len(blockedIDs)+len(recIDs))
	for _, id := range append(blockedIDs, recIDs...) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	resolved := make(map[string]line.Contact, len(ids))
	if len(ids) > 0 {
		contacts, err := a.fetchContacts(ids)
		if err != nil {
			return err
		}
		for mid, c := range contacts {
			resolved[mid] = c
		}
		var missing []string
		for _, mid := range ids {
			if _, ok := resolved[mid]; !ok {
				missing = append(missing, mid)
			}
		}
		if len(missing) > 0 {
			buddies, err := a.fetchBuddyProfiles(a.loginContext(), missing)
			if err != nil {
				return err
			}
			for mid, c := range buddies {
				resolved[mid] = c
			}
		}
	}
	rows := make([]line.Contact, 0, len(ids))
	for _, mid := range ids {
		if c, ok := resolved[mid]; ok {
			rows = append(rows, c)
		} else {
			rows = append(rows, line.Contact{Mid: mid})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		aName, bName := rows[i].EffectiveDisplayName(), rows[j].EffectiveDisplayName()
		if aName == bName {
			return rows[i].Mid < rows[j].Mid
		}
		if aName == "" {
			return false
		}
		if bName == "" {
			return true
		}
		return aName < bName
	})
	if jsonOutput {
		return a.json(rows)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(a.Out, "No blocked contacts.")
		return err
	}
	if _, err := fmt.Fprintln(a.Out, "BLOCKED CONTACT"); err != nil {
		return err
	}
	for _, c := range rows {
		name := c.EffectiveDisplayName()
		if name == "" {
			name = c.Mid
		}
		if _, err := fmt.Fprintln(a.Out, terminalText(name)); err != nil {
			return err
		}
		if showIDs && name != c.Mid {
			if _, err := fmt.Fprintln(a.Out, "  "+terminalText(c.Mid)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(a.Out, "\nShowing %d blocked contacts. Unblock someone: line contacts unblock USER_MID\n", len(rows))
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

// fetchBuddyProfiles resolves official/business account profiles for user MIDs
// omitted by GetContactsV2. Explicit not-found responses stay unavailable;
// transient, auth, storage, and cancellation errors propagate.
func (a *App) fetchBuddyProfiles(ctx context.Context, mids []string) (map[string]line.Contact, error) {
	result := make(map[string]line.Contact)
	seen := make(map[string]bool, len(mids))
	for _, mid := range mids {
		if mid == "" || seen[mid] || chatKind(mid) != "Direct" {
			continue
		}
		seen[mid] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var profile *line.BuddyProfile
		err := a.Manager.DoContext(ctx, func(api session.API) (err error) {
			profile, err = api.GetBuddyProfileContext(ctx, mid)
			return
		})
		if err != nil {
			if line.IsBuddyNotFound(session.ProtocolError(err)) {
				continue
			}
			return nil, err
		}
		if profile == nil || (profile.Mid != "" && profile.Mid != mid) {
			continue
		}
		if strings.TrimSpace(profile.DisplayName) == "" {
			continue
		}
		result[mid] = line.Contact{
			Mid:         mid,
			DisplayName: profile.DisplayName,
			PicturePath: profile.PicturePath,
		}
	}
	return result, nil
}

func (a *App) lookupContacts(mids []string, jsonOutput bool) error {
	contacts, err := a.fetchContacts(mids)
	if err != nil {
		return err
	}
	var missing []string
	for _, mid := range mids {
		if _, found := contacts[mid]; !found {
			missing = append(missing, mid)
		}
	}
	if len(missing) > 0 {
		buddies, err := a.fetchBuddyProfiles(a.loginContext(), missing)
		if err != nil {
			return err
		}
		for mid, contact := range buddies {
			contacts[mid] = contact
		}
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
