package session

import (
	"context"
	"errors"

	"github.com/kongesque/line-cli/pkg/line"
)

type LoginStage string

const (
	LoginLocal         LoginStage = "local"
	LoginBeforeScan    LoginStage = "before_scan"
	LoginPhoneApproval LoginStage = "phone_approval"
	LoginFinal         LoginStage = "final"
	LoginSetup         LoginStage = "setup"
	LoginSave          LoginStage = "save"
)

// LoginError tracks remote and local outcomes independently. Its text never
// includes the underlying error, which can contain remote response data.
type LoginError struct {
	Stage         LoginStage
	Dispatched    bool
	Approved      bool
	SaveUncertain bool
	cause         error
}

func (e *LoginError) Unwrap() error { return e.cause }

func (e *LoginError) Error() string {
	const displaced = " LINE may already have replaced the previous Chrome-style session."
	if !e.Dispatched && errors.Is(e.cause, context.Canceled) {
		return "Cancelled. Your saved session was not changed."
	}
	if e.Stage == LoginLocal {
		return "Login could not start. LINE was not contacted. Check local session storage with line auth status --check, then retry."
	}
	if e.SaveUncertain {
		return "Local storage changed, but persistence across a crash is uncertain. Run line auth status --check for local storage diagnostics." + displaced
	}
	if e.Stage == LoginSave {
		return "LINE approved login, but this device could not save the new session. Run line auth status --check for local storage diagnostics." + displaced
	}
	if e.Approved {
		return "LINE approved login, but local setup failed. The new session was not saved." + displaced
	}
	if e.Dispatched {
		return "Login's outcome could not be confirmed. Your saved session was not changed." + displaced
	}
	if e.Stage == LoginPhoneApproval {
		return "Phone approval could not be confirmed. Your saved session was not changed. Wait for the phone prompt to close, then retry line login."
	}
	if errors.Is(e.cause, line.ErrQRCodeExpired) && e.Stage == LoginBeforeScan {
		return "QR login expired after 3 attempts. Your saved session was not changed. Retry line login, or use line login --email you@example.com."
	}
	return "QR login did not complete. Your saved session was not changed. Retry line login."
}
