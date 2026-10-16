package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// MachineError asks the executable to emit one JSON error on stderr. It retains
// the cause so existing storage and signal exit codes continue to apply.
type MachineError struct {
	Detail *fault.Error
	cause  error
}

func (e *MachineError) Error() string { return e.Detail.Error() }
func (e *MachineError) Unwrap() error { return e.cause }

// Run accepts an explicit global error format before the command. Keeping it
// out of command argument scanning prevents mistaking message text for a flag.
func (a *App) Run(args []string) (err error) {
	a.errorStage = "validation"
	a.errorMutation = nil
	a.JSONErrors = false
	if len(args) > 0 && (args[0] == "--error-format" || strings.HasPrefix(args[0], "--error-format=")) {
		value := strings.TrimPrefix(args[0], "--error-format=")
		if args[0] == "--error-format" {
			if len(args) < 2 {
				return fault.New("invalid_arguments", "validation", "--error-format requires text or json", nil)
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		if value != "text" && value != "json" {
			return fault.New("invalid_arguments", "validation", "--error-format must be text or json", nil)
		}
		a.JSONErrors = value == "json"
	}
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	// These are the noninteractive core workflow commands. Login has its own
	// phone/QR interaction, and watch has its own long-lived diagnostics contract.
	if a.JSONErrors {
		if command == "auth" && len(args) > 1 && args[1] == "migrate" {
			err = fault.New("invalid_arguments", "validation", "JSON errors support auth status, not interactive migration.", nil)
			return &MachineError{Detail: DescribeError(err, "validation", nil), cause: err}
		}
		switch command {
		case "", "help", "--help", "-h", "version", "--version", "whoami", "contacts", "chats", "members", "messages", "send", "download", "react", "unsend", "auth":
		default:
			err = fault.New("invalid_arguments", "validation", "JSON errors are supported for core commands and auth status; run line help", nil)
			return &MachineError{Detail: DescribeError(err, "validation", nil), cause: err}
		}
	}
	originalOut, originalErr, interactive := a.Out, a.Err, a.Interactive
	output := &errorWriter{Writer: a.Out}
	a.Out = output
	var diagnostics diagnosticBuffer
	if a.JSONErrors {
		a.Err = &diagnostics
		a.Interactive = false
	}
	defer func() {
		a.Out, a.Err, a.Interactive = originalOut, originalErr, interactive
		if a.JSONErrors {
			if err == nil {
				err = output.firstErr
			}
			if err == nil {
				if diagnostics.Len() > 0 {
					_, err = originalErr.Write(diagnostics.Bytes())
				}
			}
			if err != nil {
				err = &MachineError{Detail: DescribeError(err, a.errorStage, a.errorMutation), cause: err}
			}
		}
	}()
	if command == "send" || command == "react" || command == "unsend" {
		a.errorMutation = &fault.Mutation{Action: command, Outcome: fault.NotAttempted}
	}
	return a.run(args)
}

// Bound suppressed diagnostics while retaining help on successful invocations.
type diagnosticBuffer struct{ bytes.Buffer }

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 64<<10 {
		b.Buffer.Write(p[:min(n, (64<<10)-b.Len())])
	}
	return n, nil
}

func (b *diagnosticBuffer) WriteString(s string) (int, error) { return b.Write([]byte(s)) }

type errorWriter struct {
	io.Writer
	firstErr error
}

func (w *errorWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		err = fault.New("output_failed", "output", "Could not write command output.", err)
		if w.firstErr == nil {
			w.firstErr = err
		}
	}
	return n, err
}

// DescribeError never copies arbitrary error strings or protocol data into the
// machine channel. Only allowlisted messages and explicit identity fields leave.
func DescribeError(err error, stage string, op *fault.Mutation) *fault.Error {
	code, message := "operation_failed", "The command could not complete."
	var typed *fault.Error
	var mutation *messaging.MutationError
	var remote *session.RemoteError
	if errors.As(err, &mutation) {
		stage = mutation.Stage
		op = &mutation.Mutation
	}
	if stage == "validation" {
		code, message = "invalid_arguments", "Invalid command arguments; see command help."
	}
	if errors.As(err, &remote) {
		code, message = "remote_failed", "LINE request failed; check connectivity and account settings."
	}
	if errors.As(err, &typed) {
		code, stage, message = typed.Code, typed.Stage, typed.Message
		if typed.Mutation != nil {
			op = typed.Mutation
		}
	}
	reason := session.StorageReason(err)
	switch {
	case typed != nil && typed.Code == "output_failed": // A confirmed receipt takes precedence over a writer's wrapped cause.
	case errors.Is(err, context.Canceled), errors.Is(err, ErrCancelled):
		code, message = "cancelled", "The command was cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		code, message = "deadline_exceeded", "The command deadline expired."
	case errors.Is(err, session.ErrNotFound):
		code, message = "no_session", "No saved LINE session; run line login."
	case errors.Is(err, session.ErrSessionInvalidated):
		code, message = "invalid_session", "The LINE session was invalidated; run line login."
	case errors.Is(err, session.ErrSessionChanged):
		code, message = "session_changed", "The saved account changed during the command."
	case errors.Is(err, session.ErrBusy):
		code, stage, message = "busy", "lock", "Another command is using the session."
	case reason != "ok" && reason != "storage_error":
		code, message = "storage_"+reason, "Session storage could not complete the operation."
	case line.IsAuthError(session.ProtocolError(err)):
		code, message = "authentication_failed", "LINE rejected authentication."
	default:
		var path *os.PathError
		if errors.As(err, &path) && stage != "output" {
			code, message = "input_failed", "Could not read the requested local input."
		}
	}
	detail := fault.New(code, stage, message, err)
	detail.Mutation = op
	if op != nil {
		switch op.Outcome {
		case fault.Unknown:
			detail.Message += " Delivery is unknown; inspect LINE before any retry."
		case fault.Rejected:
			detail.Message += " LINE rejected the message action."
		case fault.Confirmed:
			detail.Message += " The message action is confirmed; do not repeat it."
		}
		for _, step := range op.Steps {
			if step.Stage != op.Action && (step.Outcome == fault.Unknown || step.Outcome == fault.Confirmed) {
				detail.Message += " Preparation or upload may have remote effects; inspect the step outcomes."
				break
			}
		}
	}
	return detail
}

func (a *App) confirmedOutput(op *fault.Mutation, err error) error {
	a.errorMutation = op
	if err == nil {
		return nil
	}
	e := fault.New("output_failed", "output", "The message action succeeded, but its output could not be written; do not repeat it.", err)
	e.Mutation = op
	return e
}

// SignalError preserves a completed message receipt when a signal arrives after
// Run returns. Its exit status is still selected by the executable.
func (a *App) SignalError() error {
	return &MachineError{Detail: DescribeError(context.Canceled, "completion", a.errorMutation), cause: context.Canceled}
}

func (a *App) identifyMutation(chat, id string) {
	if a.errorMutation == nil {
		return
	}
	if fullChatID.MatchString(chat) || strings.HasPrefix(chat, "id:") && messaging.ValidateChatID(strings.TrimPrefix(chat, "id:")) == nil {
		a.errorMutation.ChatID = strings.TrimPrefix(chat, "id:")
	}
	if messaging.ValidateMessageID(id) == nil {
		a.errorMutation.MessageID = id
	}
}
