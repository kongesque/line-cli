package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/highesttt/matrix-line-messenger/internal/cli"
	"github.com/highesttt/matrix-line-messenger/internal/session"
)

var version = "dev"

func main() {
	ctx := context.Background()
	watching := len(os.Args) > 1 && os.Args[1] == "watch"
	if watching {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	// ReadPassword temporarily disables echo. Restore the original terminal on
	// Ctrl-C/SIGTERM as well as normal return, including during phone polling.
	if !watching && term.IsTerminal(int(os.Stdin.Fd())) {
		if state, err := term.GetState(int(os.Stdin.Fd())); err == nil {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
			go func() {
				sig := <-signals
				_ = term.Restore(int(os.Stdin.Fd()), state)
				fmt.Fprintln(os.Stderr)
				if sig == syscall.SIGTERM {
					os.Exit(143)
				}
				os.Exit(130)
			}()
		}
	}
	// Some upstream login diagnostics contain raw response bodies. Keep these
	// out of both terminal output and pipelines; CLI errors provide safe context.
	log.SetOutput(io.Discard)
	app := &cli.App{
		Context: ctx, WatchLock: session.WatchLock,
		In:  os.Stdin,
		Out: os.Stdout, Err: os.Stderr, Version: version,
		Manager: session.NewManager(session.KeychainStore{}), Lock: session.Lock,
		Password: func() (string, error) {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return "", errors.New("login requires an interactive terminal for password input")
			}
			fmt.Fprint(os.Stderr, "Password: ")
			password, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return "", errors.New("could not read password from terminal")
			}
			defer clear(password)
			if len(password) == 0 {
				return "", errors.New("password must not be empty")
			}
			return string(password), nil
		},
		Continue: func() error {
			fmt.Fprint(os.Stderr, "After approving on your phone, press Enter to continue: ")
			_, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return errors.New("phone verification cancelled")
			}
			return nil
		},
	}
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "line:", err)
		os.Exit(1)
	}
}
