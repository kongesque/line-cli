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
	_, err = fmt.Fprintln(a.Out, map[string]string{"react": "Reaction added.", "remove_reaction": "Reaction removed.", "unsend": "Message unsent."}[result.Action])
	return err
}
