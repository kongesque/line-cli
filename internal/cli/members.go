package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/messaging"
)

type memberIdentity struct {
	MID        string  `json:"mid"`
	Name       *string `json:"name"`
	NameStatus string  `json:"name_status"`
	Self       bool    `json:"self"`
}

type membershipOutput struct {
	SchemaVersion    int              `json:"schema_version"`
	Chat             namedIdentity    `json:"chat"`
	MembersComplete  bool             `json:"members_complete"`
	InviteesComplete bool             `json:"invitees_complete"`
	Members          []memberIdentity `json:"members"`
	Invitees         []memberIdentity `json:"invitees"`
}

func (a *App) membersCommand(args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("members", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line members CHAT [--json] [--timeout DURATION]\nList current group members and invitees separately, with completeness and names.\nCHAT is a group ID or unique exact name. Legacy rooms are unsupported.")
		fs.PrintDefaults()
	}
	jsonOutput := fs.Bool("json", false, "write a versioned membership snapshot as JSON")
	timeout := fs.Duration("timeout", 0, "command deadline including name resolution; 0 disables it")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line members --help")
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	if chat == "" && fs.NArg() == 1 {
		chat = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("unexpected command arguments")
	}
	if err := validateSelector(chat); err != nil {
		return err
	}
	// Reject known unsupported ID kinds before storage or name lookup.
	if fullChatID.MatchString(chat) || strings.HasPrefix(chat, "id:") {
		if err := messaging.ValidateMembershipChat(strings.TrimPrefix(chat, "id:")); err != nil {
			return err
		}
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	chat, err = a.resolveChat(chat)
	if err != nil {
		return err
	}
	snapshot, err := messaging.MembershipContext(a.loginContext(), a.Manager, chat)
	if err != nil {
		return err
	}
	state, err := a.Manager.Store.Load()
	if err != nil {
		return err
	}
	// Use the existing optional, bounded profile enrichment. Failed remote profile
	// lookups leave explicit unknown names without erasing membership evidence.
	ids := append(append([]string{}, snapshot.Members...), snapshot.Invitees...)
	names, err := a.profileNames(ids)
	if err != nil {
		return err
	}
	rows := func(ids []string) []memberIdentity {
		result := make([]memberIdentity, 0, len(ids))
		for _, mid := range ids {
			name := identity(mid, names[mid], "unavailable")
			result = append(result, memberIdentity{MID: mid, Name: name.Name, NameStatus: name.NameStatus, Self: mid == state.MID})
		}
		return result
	}
	result := membershipOutput{SchemaVersion: 1, Chat: identity(chat, snapshot.Name, "unavailable"), MembersComplete: snapshot.MembersComplete, InviteesComplete: snapshot.InviteesComplete, Members: rows(snapshot.Members), Invitees: rows(snapshot.Invitees)}
	if *jsonOutput {
		return a.json(result)
	}
	for _, list := range []struct {
		name     string
		complete bool
		rows     []memberIdentity
	}{{"Members", result.MembersComplete, result.Members}, {"Invitees", result.InviteesComplete, result.Invitees}} {
		status := "complete"
		if !list.complete {
			status = "incomplete; membership list unavailable"
		}
		if _, err := fmt.Fprintf(a.Out, "%s (%s): %d\n", list.name, status, len(list.rows)); err != nil {
			return err
		}
		for _, row := range list.rows {
			name := "(name unavailable)"
			if row.Name != nil {
				name = terminalText(*row.Name)
			}
			self := ""
			if row.Self {
				self = " (you)"
			}
			if _, err := fmt.Fprintf(a.Out, "%s  %s%s\n", terminalText(row.MID), name, self); err != nil {
				return err
			}
		}
	}
	return nil
}
