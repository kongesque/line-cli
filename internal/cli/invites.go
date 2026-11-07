package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type invitationRow struct {
	ChatID     string  `json:"chat_id"`
	Name       *string `json:"name"`
	NameStatus string  `json:"name_status"`
}

func (r invitationRow) label() string {
	if r.Name != nil {
		return *r.Name
	}
	return "(name unavailable)"
}

func (a *App) invitesCommand(args []string) error {
	sub := ""
	if len(args) > 0 && (args[0] == "accept" || args[0] == "reject") {
		sub, args = args[0], args[1:]
	}
	var chat string
	if sub != "" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	name := strings.TrimSpace("invites " + sub)
	fs := newFlagSet(name)
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	jsonOutput := fs.Bool("json", false, "write JSON")
	h := commandHelp{Command: name, Usage: []string{"line invites [options]", "line invites accept GROUP_ID [options]", "line invites reject GROUP_ID [options]"},
		About:    "List pending group invitations, or accept or reject one by full group ID.\nIn a terminal, line invites lets you choose one to accept, reject, or skip.\nAccepting joins the group; it never registers an encryption key.",
		Groups:   []flagGroup{{"Output", []string{"json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{"line invites", "line invites accept GROUP_ID", "line invites reject GROUP_ID --json"}}
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if sub != "" {
		if err := a.refuseReadOnly(sub + "ing an invitation"); err != nil {
			return err
		}
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	switch {
	case sub == "" && fs.NArg() != 0:
		return errors.New("unexpected arguments; use line invites accept GROUP_ID or line invites reject GROUP_ID")
	case sub != "" && chat == "" && fs.NArg() == 1:
		chat = fs.Arg(0)
	case sub != "" && fs.NArg() != 0:
		return errors.New("unexpected arguments; place options after the group ID")
	}
	guided := a.Interactive && !*jsonOutput
	if sub != "" && (chat != "" || !guided) {
		if err := messaging.ValidateInvitationChat(chat); err != nil {
			return err
		}
	}
	a.identifyMutation(chat, "")
	if sub != "" && chat != "" {
		return a.changeInvitation(sub, chat, "", *jsonOutput)
	}

	// List, then (in a terminal) offer a choice without holding the lock.
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	if guided {
		if err := a.rememberAccount(); err != nil {
			unlock()
			return err
		}
	}
	rows, namesComplete, err := a.pendingInvitations()
	unlock()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return a.json(struct {
			SchemaVersion int             `json:"schema_version"`
			Invitations   []invitationRow `json:"invitations"`
		}{1, rows})
	}
	if sub == "" {
		if err := a.renderInvitations(rows, namesComplete); err != nil {
			return err
		}
		if !guided || len(rows) == 0 {
			return nil
		}
		if a.readOnly {
			_, err := fmt.Fprintln(a.Err, "Read-only mode is on; accepting and rejecting are unavailable.")
			return err
		}
	} else if len(rows) == 0 {
		_, err := fmt.Fprintln(a.Out, "No pending group invitations.")
		return err
	}
	options := make([]choice, 0, len(rows))
	for _, row := range rows {
		options = append(options, choice{row.ChatID, row.label() + " · " + row.ChatID})
	}
	chat, err = a.choose("Choose an invitation", options, false)
	if err != nil {
		return err
	}
	display := ""
	for _, row := range rows {
		if row.ChatID == chat && row.Name != nil {
			display = *row.Name
		}
	}
	if sub == "" {
		sub, err = a.choose("Accept, reject, or skip this invitation?", []choice{{"accept", "Accept and join the group"}, {"reject", "Reject the invitation"}, {"skip", "Skip; change nothing"}}, false)
		if err != nil {
			return err
		}
		if sub == "skip" {
			_, err := fmt.Fprintln(a.Out, "Skipped. Nothing was changed.")
			return err
		}
	}
	a.identifyMutation(chat, "")
	return a.changeInvitation(sub, chat, display, false)
}

func (a *App) changeInvitation(sub, chat, display string, jsonOutput bool) error {
	if a.errorMutation == nil {
		a.errorMutation = &fault.Mutation{Outcome: fault.NotAttempted}
	}
	a.errorMutation.Action, a.errorMutation.ChatID = sub+"_invitation", chat
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	change := messaging.AcceptInvitationContext
	if sub == "reject" {
		change = messaging.RejectInvitationContext
	}
	result, err := change(a.loginContext(), a.Manager, chat)
	if err != nil {
		return err
	}
	op := &fault.Mutation{Action: result.Action, ChatID: result.ChatID, RequestSequence: &result.RequestSequence, Attempted: true, Outcome: fault.Confirmed}
	if jsonOutput {
		return a.confirmedOutput(op, a.json(result))
	}
	target := "the group"
	if display != "" {
		target = terminalText(display)
	}
	message := "Joined " + target + "."
	if sub == "reject" {
		message = "Rejected the invitation to " + target + "."
	}
	_, err = fmt.Fprintln(a.Out, message)
	return a.confirmedOutput(op, err)
}

// pendingInvitations lists invited groups with best-effort names. A remote
// name lookup failure leaves names unavailable instead of hiding invitations.
func (a *App) pendingInvitations() ([]invitationRow, bool, error) {
	ids, err := messaging.PendingInvitationsContext(a.loginContext(), a.Manager)
	if err != nil {
		return nil, false, err
	}
	names := make(map[string]string)
	complete := true
	for start := 0; start < len(ids); start += 20 {
		var response *line.GetChatsResponse
		err := a.Manager.DoContext(a.loginContext(), func(api session.API) (err error) {
			response, err = api.GetChatsContext(a.loginContext(), ids[start:min(start+20, len(ids))], false, false)
			return
		})
		if err == nil && response == nil {
			err = errNoNames
		}
		if fatal := enrichmentError(err); fatal != nil {
			return nil, false, fatal
		}
		if err != nil {
			complete = false
			continue
		}
		for _, chat := range response.Chats {
			names[chat.ChatMid] = chat.ChatName
		}
	}
	rows := make([]invitationRow, 0, len(ids))
	for _, id := range ids {
		named := identity(id, names[id], "unavailable")
		if named.Name == nil {
			complete = false
		}
		rows = append(rows, invitationRow{ChatID: id, Name: named.Name, NameStatus: named.NameStatus})
	}
	return rows, complete, nil
}

func (a *App) renderInvitations(rows []invitationRow, namesComplete bool) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(a.Out, "No pending group invitations.")
		return err
	}
	if _, err := fmt.Fprintf(a.Out, "Pending group invitations (%d)\n", len(rows)); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(a.Out, "  %s\n    %s\n", terminalText(row.label()), terminalText(row.ChatID)); err != nil {
			return err
		}
	}
	if !namesComplete {
		fmt.Fprintln(a.Err, "Some group names are unavailable; showing IDs.")
	}
	_, err := fmt.Fprintln(a.Out, "\nAccept: line invites accept GROUP_ID   •   Reject: line invites reject GROUP_ID")
	return err
}
