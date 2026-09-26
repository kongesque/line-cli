package main

import (
	"bufio"
	"io"
	"log"
	"os"

	"golang.org/x/term"

	"github.com/kongesque/line-cli/internal/cli"
	"github.com/kongesque/line-cli/internal/session"
)

var version = "dev"

func main() { os.Exit(run()) }

func run() int {
	signals := newCommandSignals()
	defer signals.close()
	ctx := signals.ctx
	loggingIn := len(os.Args) > 1 && os.Args[1] == "login"
	stdinFD := int(os.Stdin.Fd())
	source, closeInput := commandInput(ctx, stdinFD)
	defer closeInput()
	in := bufio.NewReader(source)
	// Some upstream login diagnostics contain raw response bodies. Keep these
	// out of both terminal output and pipelines; CLI errors provide safe context.
	log.SetOutput(io.Discard)
	app := &cli.App{
		Interactive: term.IsTerminal(stdinFD) && (loggingIn || term.IsTerminal(int(os.Stdout.Fd()))),
		Context:     ctx, WatchLock: session.WatchLock,
		In:  in,
		Out: os.Stdout, Err: os.Stderr, Version: version,
		Manager: session.NewManager(session.KeychainStore{}), Lock: session.Lock,
		Password: func() (string, error) {
			return readPassword(ctx, stdinFD, in, os.Stderr)
		},
		Continue: func() error {
			return waitForPhone(ctx, in, os.Stderr)
		},
	}
	if session.SupportsHeadless() {
		app.NewHeadlessLogin = func() (session.LoginStorage, error) { return session.BeginHeadlessLogin(ctx) }
		app.MigrateHeadless = func(accepted bool) error { return session.MigrateHeadless(ctx, accepted) }
	}
	err := app.Run(os.Args[1:])
	return commandExit(err, int(signals.code.Load()), os.Stderr)
}
