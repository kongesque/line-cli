package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/internal/update"
)

type updater interface {
	Check(context.Context) (update.Plan, error)
	Install(context.Context, update.Plan) error
}

func (a *App) updateCommand(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprint(a.Err, "Usage: line update [--check] [--json]\nInstalls the latest stable release for supported standalone installations.\nOther installations show upgrade instructions. No LINE login is required.\nStop other LINE CLI commands and watchers before installing.\n\n")
		fs.PrintDefaults()
	}
	check := fs.Bool("check", false, "check versions and show the next step without changing files")
	jsonOutput := fs.Bool("json", false, "write one JSON result; combine with --check to only check")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line update --help")
	}
	if !*check && a.readOnly {
		a.errorStage = "preparation"
		return fault.New("read_only", "preparation", "Read-only mode is on (--read-only or LINE_CLI_READ_ONLY); update was not attempted. Run line update --check to only check.", session.ErrReadOnly)
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line update --help")
	}
	u := a.Updater
	if u == nil {
		u = update.New(a.Version, a.Distribution)
	}
	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if !*jsonOutput {
		fmt.Fprintln(a.Err, "Checking for updates...")
	}
	p, err := u.Check(ctx)
	if err != nil {
		return err
	}
	if !*jsonOutput {
		if _, err := fmt.Fprintf(a.Out, "Current version: %s\nLatest version:  %s\nInstallation:    %s\nExecutable:      %s\n\n", terminalText(p.CurrentVersion), terminalText(p.LatestVersion), p.Installation, terminalText(p.Executable)); err != nil {
			return err
		}
	}
	if !*check && p.Status == "update_available" && p.CanSelfUpdate {
		err = a.installUpdate(ctx, u, p, !*jsonOutput)
		if err == nil {
			p.Status, p.Instructions, p.UpgradeCommand = "updated", "", ""
		} else if errors.Is(err, update.ErrDurability) {
			p.Status, p.Instructions = "updated_unconfirmed", err.Error()
		} else {
			p.Status, p.Instructions = "failed", err.Error()
		}
	}
	if *jsonOutput {
		if outputErr := a.json(p); outputErr != nil {
			return outputErr
		}
		return err
	}
	if err != nil {
		return err
	}
	switch p.Status {
	case "updated":
		_, err = fmt.Fprintf(a.Out, "Updated to %s. Your next line command will use the new version.\n", p.LatestVersion)
	case "up_to_date":
		_, err = fmt.Fprintln(a.Out, "You're up to date.")
	case "ahead":
		_, err = fmt.Fprintln(a.Out, "Your version is newer than the latest stable release. No changes made.")
	default:
		if p.Status == "unknown_version" {
			fmt.Fprintln(a.Out, "This build cannot be compared with stable releases. No changes made.")
		} else {
			fmt.Fprintln(a.Out, "An update is available. No changes made.")
		}
		fmt.Fprintln(a.Out, p.Instructions)
		if p.UpgradeCommand != "" {
			fmt.Fprintln(a.Out, "\n  "+terminalText(p.UpgradeCommand))
		}
		_, err = fmt.Fprintln(a.Out, "\nRelease notes: "+p.ReleaseURL)
	}
	return err
}

func (a *App) installUpdate(ctx context.Context, u updater, p update.Plan, progress bool) error {
	watchLock := a.WatchLock
	if watchLock == nil {
		watchLock = session.WatchLock
	}
	unlockWatch, err := watchLock()
	if err != nil {
		return fmt.Errorf("cannot acquire the watcher lock: %w; stop line watch and its service before updating, then try again", err)
	}
	defer unlockWatch()
	lock := a.Lock
	if lock == nil {
		lock = session.Lock
	}
	unlock, err := lock()
	if err != nil {
		return fmt.Errorf("cannot acquire the session lock: %w; stop other LINE CLI commands before updating, then try again", err)
	}
	defer unlock()
	if progress {
		fmt.Fprintln(a.Err, "Downloading and verifying the update...")
	}
	return u.Install(ctx, p)
}
