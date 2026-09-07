package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/messaging"
)

func (a *App) actionCommand(command string, args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintf(a.Err, "Usage: line %s CHAT --message ID [options]\n", command)
		fs.PrintDefaults()
	}
	id := fs.String("message", "", "target message ID (must be among the latest 100 messages)")
	jsonOutput := fs.Bool("json", false, "write JSON")
	var reaction string
	var remove bool
	if command == "react" {
		fs.StringVar(&reaction, "reaction", "", "like, love, laugh, surprise, sad, angry, or matching emoji")
		fs.BoolVar(&remove, "remove", false, "remove your reaction")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line " + command + " --help")
	}
	if chat == "" && fs.NArg() == 1 {
		chat = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("unexpected command arguments")
	}
	if err := validateSelector(chat); err != nil {
		return err
	}
	if err := messaging.ValidateMessageID(*id); err != nil {
		return err
	}
	if command == "react" {
		if remove && reaction != "" {
			return errors.New("use --reaction or --remove")
		}
		if !remove {
			if _, err := messaging.ReactionType(reaction); err != nil {
				return err
			}
		}
	}
	unlock, err := a.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	chat, err = a.resolveChat(chat)
	if err != nil {
		return err
	}
	c, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	var result *messaging.ActionResult
	if command == "react" {
		result, err = c.React(chat, *id, reaction, remove)
	} else {
		result, err = c.Unsend(chat, *id)
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		return a.json(result)
	}
	_, err = fmt.Fprintf(a.Out, "%s succeeded for message %s\n", result.Action, result.MessageID)
	return err
}
