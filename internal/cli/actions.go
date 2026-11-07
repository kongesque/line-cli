package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
)

func (a *App) actionCommand(command string, args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := newFlagSet(command)
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	h := commandHelp{Command: "react", Usage: []string{"line react CHAT --message ID (--reaction REACTION | --remove) [options]"},
		About:    "Add or remove your reaction on a message. The message must be in CHAT's\nrecent history (up to 10 pages). Each change is attempted once.",
		Groups:   []flagGroup{{"Target", []string{"message"}}, {"Reaction", []string{"reaction", "remove"}}, {"Output", []string{"json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{`line react "Alice" --message MESSAGE_ID --reaction love`, `line react "Alice" --message MESSAGE_ID --remove`}}
	if command == "unsend" {
		h = commandHelp{Command: "unsend", Usage: []string{"line unsend CHAT --message ID [options]"},
			About:    "Retract one of your own messages for everyone, subject to LINE's rules.\nIt is attempted once; check LINE before retrying an unknown outcome.",
			Groups:   []flagGroup{{"Target", []string{"message"}}, {"Output", []string{"json"}}, {"Request", []string{"timeout"}}},
			Examples: []string{`line unsend "Alice" --message MY_MESSAGE_ID`}}
	}
	id := fs.String("message", "", "target message ID (lookup: up to 10 pages / 1000 records / 30s)")
	jsonOutput := fs.Bool("json", false, "write JSON")
	var reaction string
	var remove bool
	if command == "react" {
		fs.StringVar(&reaction, "reaction", "", "like, love, laugh, surprise, sad, angry, or matching emoji")
		fs.BoolVar(&remove, "remove", false, "remove your reaction")
	}
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if err := a.refuseReadOnly(command); err != nil {
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
		return errors.New("unexpected command arguments")
	}
	guided := a.Interactive && !*jsonOutput
	if chat != "" || !guided {
		if err := validateSelector(chat); err != nil {
			return err
		}
	}
	if *id != "" || !guided {
		if err := messaging.ValidateMessageID(*id); err != nil {
			return err
		}
	}
	if command == "react" {
		if remove && reaction != "" {
			return errors.New("use --reaction or --remove")
		}
		if !remove && (reaction != "" || !guided) {
			if _, err := messaging.ReactionType(reaction); err != nil {
				return err
			}
		}
	}
	a.identifyMutation(chat, *id)
	if guided && chat != "" {
		if chat, _, err = a.chooseChat(chat); err != nil {
			return err
		}
	}
	if guided && (chat == "" || *id == "" || (command == "react" && reaction == "" && !remove)) {
		var err error
		chat, _, err = a.selectChat(chat)
		if err != nil {
			return err
		}
		if *id == "" {
			*id, err = a.selectMessage(chat, command)
			if err != nil {
				return err
			}
			if command == "unsend" {
				answer, err := a.ask("Unsend this message for everyone? [y/N]: ")
				if err != nil {
					return err
				}
				if !strings.EqualFold(strings.TrimSpace(answer), "y") {
					return ErrCancelled
				}
			}
		}
		if command == "react" && reaction == "" && !remove {
			reaction, err = a.choose("Choose a reaction", []choice{{"like", "👍 Like"}, {"love", "❤️ Love"}, {"laugh", "😆 Laugh"}, {"surprise", "😮 Surprise"}, {"sad", "😢 Sad"}, {"angry", "😡 Angry"}, {"remove", "Remove my reaction"}}, false)
			if err != nil {
				return err
			}
			if reaction == "remove" {
				reaction, remove = "", true
			}
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
		a.errorMutation.MessageID = *id
	}
	c, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	var result *messaging.ActionResult
	if command == "react" {
		result, err = c.ReactContext(a.loginContext(), chat, *id, reaction, remove)
	} else {
		result, err = c.UnsendContext(a.loginContext(), chat, *id)
	}
	if err != nil {
		return err
	}
	op := &fault.Mutation{Action: result.Action, ChatID: result.ChatID, MessageID: result.MessageID, RequestSequence: &result.RequestSequence, Attempted: true, Outcome: fault.Confirmed}
	if *jsonOutput {
		return a.confirmedOutput(op, a.json(result))
	}
	_, err = fmt.Fprintln(a.Out, map[string]string{"react": "Reaction added.", "remove_reaction": "Reaction removed.", "unsend": "Message unsent."}[result.Action])
	return a.confirmedOutput(op, err)
}
