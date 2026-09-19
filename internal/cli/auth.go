package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/session"
)

func (a *App) authCommand(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(a.Out, "Usage: line auth status [--check] [--json]\n       line auth migrate --storage=headless\nInspect or migrate local storage without contacting LINE.")
		return err
	}
	if args[0] == "migrate" {
		return a.migrateCommand(args[1:])
	}
	if args[0] != "status" {
		return errors.New("unknown auth command; run line auth --help")
	}
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var check, jsonOutput bool
	fs.BoolVar(&check, "check", false, "check local write readiness without replacing the active session")
	fs.BoolVar(&jsonOutput, "json", false, "write local status JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line auth status --help")
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
	fs := flag.NewFlagSet("auth migrate", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	storage := fs.String("storage", "", "target storage (headless; Linux only)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line auth migrate --help")
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

func (a *App) headlessLogin(email string) error {
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
	if email == "" {
		email, err = a.ask("Email: ")
		if err != nil {
			return err
		}
		email = strings.TrimSpace(email)
		if email == "" {
			return errors.New("email cannot be empty")
		}
	}
	if err := a.login(email); err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.Out, "Storage: Headless. After reboot: Expected; not verified.")
	return err
}
