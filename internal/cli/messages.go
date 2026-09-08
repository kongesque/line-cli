package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kongesque/line-cli/internal/messaging"
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
	var replyTo string
	var filePath string
	var attachment *messaging.Attachment
	showIDs := fs.Bool("show-ids", false, "show message IDs in human output")
	if command == "messages" {
		fs.IntVar(&limit, "limit", 20, "recent messages to fetch (1–100)")
	} else {
		fs.StringVar(&text, "text", "", "message text")
		fs.BoolVar(&stdin, "stdin", false, "read UTF-8 message text from stdin")
		fs.StringVar(&filePath, "file", "", "send a generic file attachment (up to 20 MiB)")
		fs.StringVar(&replyTo, "reply-to", "", "reply to this message ID")
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
	guided := a.Interactive && !*jsonOutput && !stdin
	if chat != "" || !guided {
		if err := validateSelector(chat); err != nil {
			return err
		}
	}
	promptText := false
	if limit < 1 || limit > 100 {
		return errors.New("limit must be between 1 and 100")
	}
	if command == "send" {
		if replyTo != "" {
			if err := messaging.ValidateMessageID(replyTo); err != nil {
				return err
			}
		}
		hasText, hasFile := false, false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "file" {
				hasFile = true
			}
			if f.Name == "text" {
				hasText = true
			}
		})
		sources := 0
		for _, enabled := range []bool{hasText, stdin, hasFile} {
			if enabled {
				sources++
			}
		}
		if sources != 1 && !(sources == 0 && guided) {
			return errors.New("provide exactly one of --text TEXT, --stdin, or --file PATH")
		}
		promptText = sources == 0 && guided
		if hasFile {
			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("open attachment: %w", err)
			}
			info, err := file.Stat()
			if err != nil {
				file.Close()
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > messaging.MaxAttachmentBytes {
				file.Close()
				return errors.New("attachment must be a regular file no larger than 20 MiB")
			}
			data, err := io.ReadAll(io.LimitReader(file, messaging.MaxAttachmentBytes+1))
			file.Close()
			if err != nil {
				return err
			}
			attachment = &messaging.Attachment{Name: filepath.Base(filePath), Data: data}
			if err := attachment.Validate(); err != nil {
				return err
			}
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
		if attachment == nil && !promptText {
			if err := messaging.ValidateText(text); err != nil {
				return err
			}
		}
	}
	displayChat := strings.TrimPrefix(chat, "id:")
	if guided && (chat == "" || promptText) {
		var err error
		chat, displayChat, err = a.selectChat(chat)
		if err != nil {
			return err
		}
		if promptText {
			fmt.Fprintln(a.Err, "To: "+terminalText(displayChat))
			for {
				text, err = a.ask("Message (Enter to send; Ctrl-C to cancel): ")
				if err != nil {
					return err
				}
				if strings.TrimSpace(text) != "" {
					break
				}
				fmt.Fprintln(a.Err, "Message cannot be empty.")
			}
			if err := messaging.ValidateText(text); err != nil {
				return err
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
	client, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	if command == "send" {
		var result *messaging.SendResult
		if attachment != nil {
			result, err = client.SendFile(chat, *attachment, replyTo)
		} else {
			result, err = client.SendReply(chat, text, replyTo)
		}
		if err != nil {
			return err
		}
		if *jsonOutput {
			return a.json(result)
		}
		mode := "encrypted"
		if !result.Encrypted {
			mode = "not encrypted (Letter Sealing unavailable)"
		}
		if *showIDs && result.GroupKeyRegistered {
			mode += "; new group key registered"
		} else if *showIDs && result.Encrypted && (chatKind(chat) == "Group" || chatKind(chat) == "Room") {
			mode += "; existing group key"
		}
		_, err = fmt.Fprintf(a.Out, "Sent to %s · %s\n", terminalText(displayChat), mode)
		if err == nil && *showIDs {
			_, err = fmt.Fprintln(a.Out, "ID: "+terminalText(result.ID))
		}
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
		err = a.renderMessages(displayChat, messages, *showIDs)
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d messages could not be decrypted; see each message's status/error", failed)
	}
	return nil
}
