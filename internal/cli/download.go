package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kongesque/line-cli/internal/messaging"
)

func (a *App) downloadCommand(args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := newFlagSet("download")
	timeout := fs.Duration("timeout", 2*time.Minute, "command deadline including preparation, e.g. 30s; 0 disables it")
	h := commandHelp{Command: "download", Usage: []string{"line download CHAT --message ID --output PATH [--preview N] [--json]", "line download CHAT --message ID --output - [--preview N] | COMMAND"},
		About:    "Download an image, video, audio, file, or album preview thumbnail (up to 20 MiB).\nUse line messages CHAT --details --json to discover message IDs and album\npreview counts. Existing files are never overwritten. Encrypted media is verified\nbefore the first byte is written.",
		Groups:   []flagGroup{{"Target", []string{"message", "preview"}}, {"Output", []string{"output", "json"}}, {"Request", []string{"timeout"}}},
		Examples: []string{`line download "Alice" --message MESSAGE_ID --output ./photo.jpg`, `line download "Weekend Trip" --message ALBUM_MSG_ID --preview 2 --output ./thumb.jpg`, "line download CHAT_ID --message MESSAGE_ID --output - | consumer"}}
	id := fs.String("message", "", "attachment or album message ID (lookup: up to 10 pages / 1000 records / 30s)")
	preview := fs.Int("preview", 0, "1-based album thumbnail index (defaults to 1 when omitted)")
	output := fs.String("output", "", "destination path (never overwritten), or - for binary stdout")
	jsonOutput := fs.Bool("json", false, "write JSON summary")
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
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
		return errors.New("unexpected download arguments")
	}
	previewSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "preview" {
			previewSet = true
		}
	})
	if previewSet && *preview < 1 {
		return errors.New("--preview must be a positive integer starting at 1")
	}
	if err := a.validateDownloadOutput(*output, *jsonOutput); err != nil {
		return err
	}
	guided := a.Interactive && !*jsonOutput && *output != "-"
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
	if *output == "" && !guided {
		return errors.New("download requires --output PATH")
	}
	if guided && chat != "" {
		if chat, _, err = a.chooseChat(chat); err != nil {
			return err
		}
	}
	if guided && (chat == "" || *id == "" || *output == "") {
		var err error
		chat, _, err = a.selectChat(chat)
		if err != nil {
			return err
		}
		if *id == "" {
			selected, err := a.selectMessageDetail(chat, "download")
			if err != nil {
				return err
			}
			*id = selected.ID
			if !previewSet && selected.Content.Post != nil && selected.Content.Post.Type == "album" && selected.Content.Post.PreviewCount != nil && *selected.Content.Post.PreviewCount > 1 {
				maxPreview := *selected.Content.Post.PreviewCount
				answer, err := a.ask(fmt.Sprintf("Preview number (1-%d) [1]: ", maxPreview))
				if err != nil {
					return err
				}
				answer = strings.TrimSpace(answer)
				if answer == "" {
					*preview = 1
					previewSet = true
				} else {
					n, err := strconv.Atoi(answer)
					if err != nil || n < 1 || n > maxPreview {
						return fmt.Errorf("choose a preview number between 1 and %d", maxPreview)
					}
					*preview = n
					previewSet = true
				}
			}
		}
		if *output == "" {
			*output, err = a.ask("Save file to: ")
			if err != nil {
				return err
			}
			if *output == "" {
				return errors.New("output path cannot be empty")
			}
			if err := a.validateDownloadOutput(*output, *jsonOutput); err != nil {
				return err
			}
			if *output == "-" {
				return errors.New("binary stdout requires explicit options: line download CHAT --message ID --output -")
			}
		}
	}
	var f *os.File
	if *output != "-" {
		if _, err := os.Lstat(*output); err == nil {
			return errors.New("output already exists; choose a new path")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var err error
		f, err = os.CreateTemp(filepath.Dir(*output), ".line-download-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		defer f.Close()
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
	ctx := a.loginContext()
	var opts messaging.DownloadOptions
	if previewSet {
		p := *preview
		opts.Preview = &p
	}
	data, err := c.DownloadFileWithOptions(ctx, chat, *id, opts)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if *output == "-" {
		n, err := a.Out.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Same-directory linking atomically publishes the complete file and fails
	// if another process created the destination while the download was running.
	if err = os.Link(f.Name(), *output); err != nil {
		return fmt.Errorf("save downloaded file without overwriting: %w", err)
	}
	if *jsonOutput {
		return a.json(struct {
			Path      string `json:"path"`
			Bytes     int    `json:"bytes"`
			MessageID string `json:"message_id"`
		}{*output, len(data), *id})
	}
	_, err = fmt.Fprintf(a.Out, "Saved %d bytes to %s\n", len(data), terminalText(*output))
	return err
}

func (a *App) validateDownloadOutput(output string, jsonOutput bool) error {
	if output != "-" {
		return nil
	}
	if jsonOutput {
		return errors.New("--output - cannot be combined with --json; binary bytes and a JSON summary cannot share stdout")
	}
	if a.StdoutIsTerminal {
		return errors.New("refusing to write binary data to a terminal; use --output PATH or pipe stdout to another command")
	}
	return nil
}
