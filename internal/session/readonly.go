package session

import (
	"errors"

	"github.com/kongesque/line-cli/internal/fault"
)

// ErrReadOnly identifies a remote change refused because read-only mode is on.
var ErrReadOnly = errors.New("read-only mode blocks remote changes")

// ReadOnlyError is the typed refusal shared by the CLI gate and the Manager
// guard. It names the requested action without command input or protocol data.
func ReadOnlyError(action string) error {
	if action == "" {
		action = "the remote change"
	}
	return fault.New("read_only", "preparation", "Read-only mode is on (--read-only or LINE_CLI_READ_ONLY); "+action+" was not attempted.", ErrReadOnly)
}

// readOnly reports the guard error before credentials, the network, or a
// persisted request sequence are touched.
func (m *Manager) readOnly(action string) error {
	if m.ReadOnly {
		return ReadOnlyError(action)
	}
	return nil
}
