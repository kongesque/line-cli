package cli

import (
	"context"
	"errors"
	"time"
)

// commandDeadline covers selection, input, authentication, keys, and transfer.
// Restore the caller's context so an App can safely run another command.
func (a *App) commandDeadline(timeout time.Duration) (func(), error) {
	if timeout < 0 {
		return nil, errors.New("timeout must be 0 or greater")
	}
	if timeout == 0 {
		return func() {}, nil
	}
	previous := a.Context
	ctx, cancel := context.WithTimeout(a.loginContext(), timeout)
	a.Context = ctx
	return func() { cancel(); a.Context = previous }, nil
}
