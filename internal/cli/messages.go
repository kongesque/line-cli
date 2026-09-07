package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/highesttt/matrix-line-messenger/internal/messaging"
)

func (a *App) messageCommand(command string, args []string) error {
	var chat string
	// Support the documented positional-first form as well as flags before ID.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintf(a.Err, "Usage: line %s CHAT [options]\nCHAT is a full ID or unique exact name. Quote names with spaces.\n", command)
		fs.PrintDefaults()
	}
	jsonOutput := fs.Bool("json", false, "write JSON to stdout")
	limit := 20
	var text string
	var stdin bool
	if command == "messages" {
		fs.IntVar(&limit, "limit", 20, "recent messages to fetch (1–100)")
	} else {
		fs.StringVar(&text, "text", "", "message text")
		fs.BoolVar(&stdin, "stdin", false, "read UTF-8 message text from stdin")
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
		return errors.New("unexpected arguments; place options after the chat name or ID")
	}
	if err := validateSelector(chat); err != nil {
		return err
	}
	if limit < 1 || limit > 100 {
		return errors.New("limit must be between 1 and 100")
	}
	if command == "send" {
		hasText := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "text" {
				hasText = true
			}
		})
		if hasText == stdin {
			return errors.New("provide exactly one of --text TEXT or --stdin")
		}
		if stdin {
			if a.In == nil {
				return errors.New("stdin is unavailable")
			}
			data, err := io.ReadAll(io.LimitReader(a.In, messaging.MaxTextUnits*4+1))
			if err != nil {
				return errors.New("could not read message from stdin")
			}
			if len(data) > messaging.MaxTextUnits*4 {
				return errors.New("stdin message exceeds the CLI size limit")
			}
			text = string(data)
		}
		if err := messaging.ValidateText(text); err != nil {
			return err
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
	client, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	if command == "send" {
		result, err := client.Send(chat, text)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return a.json(result)
		}
		mode := "Letter Sealing"
		if !result.Encrypted {
			mode = "plaintext: Letter Sealing unavailable"
		}
		if result.GroupKeyRegistered {
			mode += "; new group key registered"
		} else if result.Encrypted && (chatKind(chat) == "Group" || chatKind(chat) == "Room") {
			mode += "; existing group key"
		}
		_, err = fmt.Fprintf(a.Out, "Sent %s (%s)\n", terminalText(result.ID), mode)
		return err
	}
	messages, err := client.History(chat, limit)
	if err != nil {
		return err
	}
	failed := 0
	for _, message := range messages {
		if message.Status == "decryption_failed" {
			failed++
		}
	}
	if *jsonOutput {
		err = a.json(messages)
	} else {
		tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tFROM\tSTATUS\tTEXT")
		for _, message := range messages {
			text := message.Text
			if message.Status == "unsupported" {
				text = fmt.Sprintf("[content type %d]", message.ContentType)
			}
			if message.Error != "" {
				text = "[" + message.Error + "]"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", terminalText(message.ID), terminalText(message.From), message.Status, terminalText(text))
		}
		err = tw.Flush()
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d messages could not be decrypted; see each message's status/error", failed)
	}
	return nil
}
