package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/session"
)

func (a *App) authCommand(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(a.Out, `Usage: line auth status [--check] [--json]
       line auth migrate --storage=headless

Inspect or migrate local session storage. Neither subcommand contacts LINE.

Subcommands:
  status    Show where the session is stored and whether it can be read.
            Add --check to also test write readiness without replacing the
            active session, or --json for a status object (also on errors).
  migrate   Linux only: move an existing native session to headless storage.
            Needs a terminal to accept host-only protection.

Use line doctor to also check whether LINE still accepts the session.

Examples:
  line auth status
  line auth status --check --json
  line auth migrate --storage=headless`)
		return err
	}
	if args[0] == "migrate" {
		return a.migrateCommand(args[1:])
	}
	if args[0] != "status" {
		if best := closest(args[0], []string{"status", "migrate"}); best != "" {
			return fmt.Errorf("unknown auth command %q; did you mean line auth %s? Run line auth --help", safeWord(args[0]), best)
		}
		return errors.New("unknown auth command; run line auth --help")
	}
	fs := newFlagSet("auth status")
	var check, jsonOutput bool
	fs.BoolVar(&check, "check", false, "check local write readiness without replacing the active session")
	fs.BoolVar(&jsonOutput, "json", false, "write local status JSON")
	h := commandHelp{Command: "auth status", Usage: []string{"line auth status [--check] [--json]"},
		About:    "Show where the session is stored and whether it can be read. Does not contact\nLINE or prove that the saved login still works; line doctor checks that.",
		Groups:   []flagGroup{{"Checks", []string{"check"}}, {"Output", []string{"json"}}},
		Examples: []string{"line auth status", "line auth status --check --json"}}
	if shown, err := a.parseFlags(fs, h, args[1:]); shown || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line auth status --help")
	}
	inspector, ok := a.Manager.Store.(interface {
		Status(bool) (session.StorageStatus, error)
	})
	if !ok {
		return errors.New("local storage status is unavailable")
	}
	a.errorStage = "storage"
	unlock, err := a.Lock()
	if err != nil {
		if jsonOutput {
			if outputErr := a.json(session.StorageStatus{Schema: 1, Configured: "unknown", Backend: "unknown", Protection: "unverified", ReadAccess: "unknown", WriteAccess: "not_checked", Reboot: "not_verified", LINEValidity: "not_checked", Reason: session.StorageReason(err)}); outputErr != nil {
				return outputErr
			}
		}
		return err
	}
	status, statusErr := inspector.Status(check)
	unlock()
	if jsonOutput {
		if err := a.json(status); err != nil {
			return err
		}
		return statusErr
	}
	protection := status.Protection
	if protection == "host-user" {
		protection = "Host key; no TPM"
	}
	if protection == "host-tpm2-user" {
		protection = "Host key + TPM binding; hardware type unverified"
	}
	if !status.ProtectionVerified {
		protection += " (unverified)"
	}
	fmt.Fprintf(a.Out, "Session: %s\nStorage: %s\nProtection: %s\nRead access: %s\nWrite access: %s\n", status.Configured, status.Backend, protection, status.ReadAccess, status.WriteAccess)
	if status.Email != "" {
		fmt.Fprintf(a.Out, "Saved account: %s\n", terminalText(status.Email))
	}
	if status.PCRBank != 0 || status.FixedPCR != 0 || status.SignedPCR != 0 {
		fmt.Fprintf(a.Out, "Reported fixed PCR mask: %#x\nReported PCR bank: %d\nReported signed PCR mask: %#x\n", status.FixedPCR, status.PCRBank, status.SignedPCR)
	}
	reboot := "Not verified"
	if status.Reboot == "expected_not_verified" {
		reboot = "Expected; not verified"
	}
	_, err = fmt.Fprintf(a.Out, "After reboot: %s\nLINE validity: Not checked\nReason: %s\n", reboot, status.Reason)
	if err != nil {
		return err
	}
	return statusErr
}

func (a *App) migrateCommand(args []string) error {
	fs := newFlagSet("auth migrate")
	storage := fs.String("storage", "", "target storage (headless; Linux only)")
	h := commandHelp{Command: "auth migrate", Usage: []string{"line auth migrate --storage=headless"},
		About:    "Linux only: move an accessible native session to headless storage, keeping\ntokens, keys, request sequence, and the watch position. Requires a terminal to\naccept host-only protection. Stop watchers and automation first.",
		Groups:   []flagGroup{{"Storage", []string{"storage"}}},
		Examples: []string{"line auth migrate --storage=headless"}}
	if shown, err := a.parseFlags(fs, h, args); shown || err != nil {
		return err
	}
	if err := a.refuseReadOnly("storage migration"); err != nil {
		return err
	}
	if fs.NArg() != 0 || *storage != "headless" {
		return errors.New("use line auth migrate --storage=headless")
	}
	if a.MigrateHeadless == nil {
		return session.ErrHeadlessUnsupported
	}
	if !a.Interactive {
		return errors.New("storage migration requires an interactive terminal for protection acceptance")
	}
	fmt.Fprintln(a.Err, "Migrate local storage to Host key; no TPM. A complete disk copy can include the decryption secret. Root and malware running as this account remain outside the protection boundary. Stop automation and older CLI versions before migrating. Reboot access is expected, not verified.")
	answer, err := a.ask("Accept host-only protection? Type yes to continue: ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) != "yes" {
		return ErrCancelled
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := a.MigrateHeadless(true); err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.Out, "Storage: Headless. Migration and native-key cleanup complete. After reboot: Expected; not verified.")
	return err
}

func (a *App) headlessLogin(options loginOptions) error {
	if a.NewHeadlessLogin == nil {
		return session.ErrHeadlessUnsupported
	}
	if !a.Interactive {
		return errors.New("headless enrollment requires an interactive terminal")
	}
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	prepared, err := a.NewHeadlessLogin()
	unlock()
	if err != nil {
		return err
	}
	defer prepared.Close()
	if prepared.RequiresHostConsent() {
		fmt.Fprintln(a.Err, "Headless protection: Host key; no TPM. A complete disk copy can include the decryption secret. This does not protect against root or malware running as this account. Reboot access is expected, not verified.")
		answer, err := a.ask("Accept host-only protection? Type yes to continue: ")
		if err != nil {
			return err
		}
		if strings.TrimSpace(answer) != "yes" {
			return ErrCancelled
		}
		prepared.AcceptHost()
	}
	originalStore, originalStorage := a.Manager.Store, a.Manager.Storage
	a.Manager.Store, a.Manager.Storage = prepared, prepared
	defer func() { a.Manager.Store, a.Manager.Storage = originalStore, originalStorage }()
	signedIn, err := a.performLogin(options)
	if err != nil || !signedIn {
		return err
	}
	_, err = fmt.Fprintln(a.Out, "Storage: Headless. After reboot: Expected; not verified.")
	return err
}
