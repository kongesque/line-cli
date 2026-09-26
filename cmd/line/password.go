package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/term"
)

func waitForPhone(ctx context.Context, in *bufio.Reader, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprint(out, "After approving on your phone, press Enter to continue: "); err != nil {
		return err
	}
	_, err := in.ReadString('\n')
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("phone verification cancelled")
	}
	return nil
}

// Terminal state changes belong to the command goroutine, never the worker
// blocked in a console read. Restoration finishes before main calls os.Exit.
func readPassword(ctx context.Context, fd int, in io.Reader, out io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !term.IsTerminal(fd) {
		return "", errors.New("login requires an interactive terminal for password input")
	}
	state, err := term.GetState(fd)
	if err != nil {
		return "", errors.New("could not read terminal state")
	}
	if err := disableEcho(fd); err != nil {
		return "", errors.New("could not disable terminal echo")
	}
	defer term.Restore(fd, state)
	if _, err := fmt.Fprint(out, "Password: "); err != nil {
		return "", err
	}
	defer fmt.Fprintln(out)
	password, err := readPasswordLine(ctx, in)
	if err != nil {
		return "", err
	}
	if len(password) == 0 {
		return "", errors.New("password must not be empty")
	}
	return password, nil
}

func readPasswordLine(ctx context.Context, in io.Reader) (string, error) {
	password := make([]byte, 0, 4096)
	defer func() { clear(password) }()
	var b [1]byte
	defer clear(b[:])
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := in.Read(b[:])
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if n != 0 {
			switch b[0] {
			case '\n':
				return string(password), nil
			case '\r':
				// Canonical console input normalizes Enter to LF or CRLF.
			case '\b', 127:
				if len(password) > 0 {
					password[len(password)-1] = 0
					password = password[:len(password)-1]
				}
			default:
				if len(password) >= 4096 {
					return "", errors.New("password exceeds the CLI size limit")
				}
				password = append(password, b[0])
			}
		}
		if err != nil {
			return "", err
		}
	}
}
