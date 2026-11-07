package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
)

// readCommand is the only command that sends read receipts.
func (a *App) readCommand(args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line read CHAT [--message ID] [--json]\nMark a chat as read. Others in the chat will see \"Read\".\nCHAT is a full ID or unique exact name. Without --message, marks read up to the\nnewest message from someone else among the latest 50. No other command marks\nmessages as read.")
		fs.PrintDefaults()
	}
	id := fs.String("message", "", "mark read up to this message from someone else (lookup: up to 10 pages / 1000 records / 30s)")
	jsonOutput := fs.Bool("json", false, "write JSON")
	showIDs := fs.Bool("show-ids", false, "show the message ID in human output")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line read --help")
	}
	if err := a.refuseReadOnly("read"); err != nil {
		return err
	}
	restore, err := a.commandDeadline(*timeout)
	if err != nil {
		return err
	}
	defer restore()
	if chat == "" && fs.NArg() == 1 {
		chat = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("unexpected arguments; place options after the chat name or ID")
	}
	guided := a.Interactive && !*jsonOutput
	if chat != "" || !guided {
		if err := validateSelector(chat); err != nil {
			return err
		}
	}
	if *id != "" {
		if err := messaging.ValidateMessageID(*id); err != nil {
			return err
		}
	}
	a.identifyMutation(chat, *id)
	displayChat := strings.TrimPrefix(chat, "id:")
	if guided && chat == "" {
		chat, displayChat, err = a.selectChatWith("", true)
		if err != nil {
			return err
		}
		answer, err := a.ask("Mark " + terminalText(displayChat) + " as read? Others in the chat will see \"Read\". [y/N]: ")
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			return ErrCancelled
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
	if a.errorMutation != nil {
		a.errorMutation.ChatID = chat
	}
	client, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	result, err := client.MarkReadContext(a.loginContext(), chat, *id)
	if err != nil {
		return err
	}
	op := &fault.Mutation{Action: result.Action, ChatID: result.ChatID, MessageID: result.MessageID, RequestSequence: &result.RequestSequence, Attempted: true, Outcome: fault.Confirmed}
	if *jsonOutput {
		return a.confirmedOutput(op, a.json(result))
	}
	upTo := "the newest message from others"
	if *id != "" {
		upTo = "the chosen message"
	}
	_, err = fmt.Fprintf(a.Out, "Marked as read in %s up to %s. Others in the chat can now see \"Read\".\n", terminalText(displayChat), upTo)
	if err == nil && *showIDs {
		_, err = fmt.Fprintln(a.Out, "ID: "+terminalText(result.MessageID))
	}
	return a.confirmedOutput(op, err)
}
