package main

import (
	"bufio"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/kongesque/line-cli/internal/cli"
	"github.com/kongesque/line-cli/internal/session"
)

var version = "dev"

// Only official release builds opt into standalone updates; source builds keep
// their installation method even when their version matches a published tag.
var distribution = "source"

func main() { os.Exit(run()) }

func run() int {
	signals := newCommandSignals()
	defer signals.close()
	ctx := signals.ctx
	commandArgs := os.Args[1:]
	if len(commandArgs) > 1 && commandArgs[0] == "--error-format" {
		commandArgs = commandArgs[2:]
	} else if len(commandArgs) > 0 && strings.HasPrefix(commandArgs[0], "--error-format=") {
		commandArgs = commandArgs[1:]
	}
	loggingIn := len(commandArgs) > 0 && commandArgs[0] == "login"
	stdinFD := int(os.Stdin.Fd())
	source, closeInput := commandInput(ctx, stdinFD)
	defer closeInput()
	in := bufio.NewReader(source)
	// Some upstream login diagnostics contain raw response bodies. Keep these
	// out of both terminal output and pipelines; CLI errors provide safe context.
	log.SetOutput(io.Discard)
	app := &cli.App{
		Interactive:      term.IsTerminal(stdinFD) && (loggingIn || term.IsTerminal(int(os.Stdout.Fd()))),
		StdoutIsTerminal: term.IsTerminal(int(os.Stdout.Fd())),
		Context:          ctx, WatchLock: session.WatchLock,
		In:  in,
		Out: os.Stdout, Err: os.Stderr, Version: version, Distribution: distribution,
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
	code := int(signals.code.Load())
	if err == nil && code != 0 && app.JSONErrors {
		err = app.SignalError()
	}
	return commandExit(err, code, os.Stderr)
}
