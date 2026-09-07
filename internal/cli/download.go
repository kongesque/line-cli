package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kongesque/line-cli/internal/messaging"
)

func (a *App) downloadCommand(args []string) error {
	var chat string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		chat, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line download CHAT --message ID --output PATH [--json]")
		fs.PrintDefaults()
	}
	id := fs.String("message", "", "file message ID (within the latest 100 messages)")
	output := fs.String("output", "", "destination file; existing files are never overwritten")
	jsonOutput := fs.Bool("json", false, "write JSON summary")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid download options")
	}
	if chat == "" && fs.NArg() == 1 {
		chat = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return errors.New("unexpected download arguments")
	}
	if err := validateSelector(chat); err != nil {
		return err
	}
	if err := messaging.ValidateMessageID(*id); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("download requires --output PATH")
	}
	if _, err := os.Lstat(*output); err == nil {
		return errors.New("output already exists; choose a new path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(*output), ".line-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
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
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	data, err := c.DownloadFile(ctx, chat, *id)
	if err != nil {
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
