package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/input"
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
	timeout := fs.Duration("timeout", 0, "command deadline including preparation, e.g. 30s; 0 disables it")
	fs.Usage = func() {
		fmt.Fprintf(a.Err, "Usage: line %s CHAT [options]\nCHAT is a full ID or unique exact name. Quote names with spaces.\n", command)
		fs.PrintDefaults()
	}
	jsonOutput := fs.Bool("json", false, "write JSON to stdout")
	limit := 20
	var pageMode bool
	var details, resolveNames bool
	var cursor string
	var text string
	var stdin bool
	var replyTo string
	var filePath, imagePath string
	var preparedImage *messaging.PreparedImage
	var mentionsJSON string
	var mentions []messaging.Mention
	var mentionsSet bool
	var attachment *messaging.Attachment
	showIDs := fs.Bool("show-ids", false, "show message IDs in human output")
	if command == "messages" {
		fs.BoolVar(&details, "details", false, "include safe decoded content in a versioned envelope (requires --json)")
		fs.BoolVar(&resolveNames, "resolve-names", false, "resolve sender names in batches (requires --details --json)")
		fs.IntVar(&limit, "limit", 20, "maximum messages to fetch (1–100)")
		fs.BoolVar(&pageMode, "page", false, "return a JSON history page with continuation cursor (requires --json)")
		fs.StringVar(&cursor, "cursor", "", "continue a history page (requires --page --json)")
	} else {
		fs.StringVar(&mentionsJSON, "mentions", "", "explicit JSON mention array: [{\"start\":0,\"end\":5,\"mid\":\"USER_MID\"}] (UTF-16 offsets; text/stdin only)")
		fs.StringVar(&text, "text", "", "message text")
		fs.BoolVar(&stdin, "stdin", false, "read UTF-8 message text from stdin")
		fs.StringVar(&filePath, "file", "", "send a generic file attachment (up to 20 MiB)")
		fs.StringVar(&imagePath, "image", "", "send a native still image (JPEG/PNG, up to 20 MiB / 40 million pixels; converted to JPEG; no --reply-to)")
		fs.StringVar(&replyTo, "reply-to", "", "reply to a verified message (lookup: up to 10 pages / 1000 records / 30s)")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line " + command + " --help")
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
	guided := a.Interactive && !*jsonOutput && !stdin
	if chat != "" || !guided {
		if err := validateSelector(chat); err != nil {
			return err
		}
	}
	if details && !*jsonOutput {
		return errors.New("--details requires --json")
	}
	if resolveNames && !details {
		return errors.New("--resolve-names requires --details --json")
	}
	if pageMode && !*jsonOutput {
		return errors.New("--page requires --json")
	}
	cursorSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "mentions" {
			mentionsSet = true
		}
		if f.Name == "cursor" {
			cursorSet = true
		}
	})
	if cursorSet {
		if !pageMode || !*jsonOutput {
			return errors.New("--cursor requires --page --json")
		}
		cursorChat := strings.TrimPrefix(chat, "id:")
		if !fullChatID.MatchString(chat) && !strings.HasPrefix(chat, "id:") {
			cursorChat = ""
		}
		if err := messaging.ValidateHistoryCursor(cursor, cursorChat); err != nil {
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
		hasText, hasFile, hasImage := false, false, false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "image" {
				hasImage = true
			}
			if f.Name == "file" {
				hasFile = true
			}
			if f.Name == "text" {
				hasText = true
			}
		})
		sources := 0
		for _, enabled := range []bool{hasText, stdin, hasFile, hasImage} {
			if enabled {
				sources++
			}
		}
		if sources != 1 && !(sources == 0 && guided) {
			return errors.New("provide exactly one of --text TEXT, --stdin, --file PATH, or --image PATH")
		}
		if mentionsSet {
			if hasFile || hasImage || (!hasText && !stdin) {
				return errors.New("--mentions requires --text or --stdin and cannot be combined with --file or --image")
			}
			mentions, err = messaging.ParseMentions(mentionsJSON)
			if err != nil {
				return err
			}
		}
		if hasImage && replyTo != "" {
			return errors.New("--image cannot be combined with --reply-to; image reply support is unverified")
		}
		promptText = sources == 0 && guided
		if hasFile || hasImage {
			if hasImage {
				filePath = imagePath
			}
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
			if hasImage {
				preparedImage, err = messaging.PrepareImage(a.loginContext(), *attachment)
				if err != nil {
					return err
				}
			}
		}

		if stdin {
			if a.In == nil {
				return errors.New("stdin is unavailable")
			}
			data, err := io.ReadAll(io.LimitReader(input.NewReader(a.loginContext(), a.In), messaging.MaxTextUnits*4+1))
			if err != nil {
				return fmt.Errorf("could not read message from stdin: %w", err)
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
	if mentionsSet {
		if _, err := messaging.EncodeMentions(text, mentions); err != nil {
			return err
		}
		if fullChatID.MatchString(chat) || strings.HasPrefix(chat, "id:") {
			id := strings.TrimPrefix(chat, "id:")
			for _, mention := range mentions {
				if mention.All && strings.HasPrefix(strings.ToLower(id), "u") {
					return errors.New("mention-all requires a group or room")
				}
			}
		}
	}
	a.identifyMutation(chat, "")
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
	if a.errorMutation != nil {
		a.errorMutation.ChatID = chat
	}
	client, err := messaging.New(a.Manager)
	if err != nil {
		return err
	}
	if command == "send" {
		var result *messaging.SendResult
		if preparedImage != nil {
			result, err = client.SendImageContext(a.loginContext(), chat, *preparedImage, replyTo)
		} else if attachment != nil {
			result, err = client.SendFileContext(a.loginContext(), chat, *attachment, replyTo)
		} else {
			result, err = client.SendMentionsContext(a.loginContext(), chat, text, replyTo, mentions)
		}
		if err != nil {
			return err
		}
		op := &fault.Mutation{Action: "send", ChatID: result.ChatID, MessageID: result.ID, RequestSequence: &result.RequestSequence, Attempted: true, Outcome: fault.Confirmed}
		if *jsonOutput {
			return a.confirmedOutput(op, a.json(result))
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
		return a.confirmedOutput(op, err)
	}
	var messages []messaging.Message
	var page *messaging.HistoryPage
	if pageMode {
		page, err = client.HistoryPageContext(a.loginContext(), chat, limit, cursor)
		if err == nil {
			messages = page.Messages
		}
	} else {
		messages, err = client.HistoryContext(a.loginContext(), chat, limit)
	}
	if err != nil {
		return err
	}
	failed := 0
	for _, message := range messages {
		if (!details && message.Status == "decryption_failed") || (details && message.Content.Status == "decryption_failed") {
			failed++
		}
	}
	if *jsonOutput {
		if details {
			var result *detailedHistory
			result, err = a.detailedMessages(chat, messages, page, resolveNames)
			if err == nil {
				err = a.json(result)
			}
		} else if pageMode {
			err = a.json(page)
		} else {
			err = a.json(messages)
		}
	} else {
		err = a.renderMessages(displayChat, messages, *showIDs)
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fault.New("decryption_failed", "decode", fmt.Sprintf("%d messages could not be decrypted; see each message's status/error", failed), nil)
	}
	return nil
}
