package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/kongesque/line-cli/internal/session"
)

type loginOptions struct {
	email                  string
	qrURL, force, headless bool
}

func (a *App) loginCommand(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() {
		fmt.Fprintln(a.Err, "Usage: line login [--qr | --email ADDRESS] [--qr-url] [--force] [--headless]")
		fs.PrintDefaults()
	}
	var options loginOptions
	var qr bool
	fs.StringVar(&options.email, "email", "", "use email/password login with this address")
	fs.BoolVar(&qr, "qr", false, "sign in using a QR code (default)")
	fs.BoolVar(&options.qrURL, "qr-url", false, "show the sensitive one-time URL instead of a terminal QR code")
	fs.BoolVar(&options.force, "force", false, "skip saved-session replacement confirmation")
	fs.BoolVar(&options.headless, "headless", false, "Linux: enroll headless host-key storage; preserve existing protection")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid options; run line login --help. LINE was not contacted")
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments; run line login --help. LINE was not contacted")
	}
	emailSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "email" {
			emailSet = true
		}
	})
	options.email = strings.TrimSpace(options.email)
	if emailSet && (qr || options.qrURL) {
		return errors.New("--email cannot be combined with --qr or --qr-url. LINE was not contacted")
	}
	if emailSet && options.email == "" {
		return errors.New("--email requires a nonempty address. LINE was not contacted")
	}
	if options.headless && a.NewHeadlessLogin == nil {
		return loginLocalError(session.ErrHeadlessUnsupported)
	}
	if (options.email == "" || options.headless) && !a.Interactive {
		return errors.New("QR login and headless enrollment require an interactive terminal. LINE was not contacted")
	}
	if options.email == "" && !options.qrURL {
		if err := qrDebugCheck(); err != nil {
			return loginLocalError(err)
		}
	}
	if options.headless {
		return a.headlessLogin(options)
	}
	_, err := a.performLogin(options)
	return err
}

func loginLocalError(err error) error {
	return fmt.Errorf("%w. LINE was not contacted", err)
}

func (a *App) performLogin(options loginOptions) (bool, error) {
	if err := a.prepareLogin(); err != nil {
		return false, loginLocalError(err)
	}
	keep, err := a.confirmLoginReplacement(options.force)
	if err != nil {
		return false, loginLocalError(err)
	}
	if keep {
		_, err := fmt.Fprintln(a.Out, "Kept the existing session.")
		return false, err
	}
	if _, err := fmt.Fprintln(a.Err, "Note: LINE allows one Chrome-style session. Signing in here may sign out LINE\nfor Chrome or another Chrome-style client."); err != nil {
		return false, loginLocalError(err)
	}
	if options.email != "" {
		err = a.loginEmail(options.email)
	} else {
		err = a.loginQR(options)
	}
	return err == nil, err
}

// The snapshot precedes all input, including confirmation. Even --force checks
// for session creation or storage changes before remote authentication.
func (a *App) confirmLoginReplacement(force bool) (bool, error) {
	unlock, err := a.Lock()
	if err != nil {
		return false, err
	}
	if err := a.Manager.CheckLogin(a.loginContext(), a.loginSnapshot); err != nil {
		unlock()
		return false, err
	}
	s, err := a.Manager.Store.Load()
	unlock()
	if errors.Is(err, session.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s == nil {
		return false, errors.New("saved session is invalid")
	}
	if force || s.Invalidated || s.AccessToken == "" {
		return false, nil
	}
	if !a.Interactive {
		return false, errors.New("a LINE session is already saved; run login in a terminal to confirm replacement, or use --force")
	}
	label := "A LINE session is already saved on this device."
	if s.Email != "" {
		label = "A LINE session for " + terminalText(s.Email) + " is already saved on this device."
	}
	if _, err := fmt.Fprintln(a.Err, label+"\n\nSigning in again may replace the active LINE Chrome-style session."); err != nil {
		return false, err
	}
	answer, err := a.ask("Continue? [y/N]: ")
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(strings.TrimSpace(answer), "y"), nil
}

func (a *App) loginQR(options loginOptions) error {
	unlock, err := a.lock()
	if err != nil {
		return loginLocalError(err)
	}
	defer unlock()
	if err := a.Manager.CheckLogin(a.loginContext(), a.loginSnapshot); err != nil {
		return loginLocalError(err)
	}
	ctx, cancel := context.WithCancel(a.loginContext())
	defer cancel()
	p := newQRPresenter(a, options.qrURL, cancel)
	p.start(ctx)
	login := a.LoginQR
	if login == nil {
		login = a.Manager.LoginQR
	}
	profile, err := login(ctx, p.event)
	p.close()
	if err != nil {
		var width *qrWidthError
		if errors.As(err, &width) {
			return fmt.Errorf("%s Login did not complete; your saved session was not changed", width.Error())
		}
		return err
	}
	if profile == nil {
		return errors.New("QR login returned no completed profile")
	}
	_, err = fmt.Fprintf(a.Out, "Signed in as %s. Session saved securely.\nNext: line chats\n", terminalText(profile.DisplayName))
	return err
}
