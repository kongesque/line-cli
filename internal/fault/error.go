// Package fault defines the safe, versioned command error contract. Causes are
// retained for errors.Is/As, but are never serialized or used as safe messages.
package fault

type Outcome string

const (
	NotAttempted Outcome = "not_attempted"
	Confirmed    Outcome = "confirmed"
	Rejected     Outcome = "rejected"
	Unknown      Outcome = "unknown"
)

// Attempted means handed to the mutation API. After that point, failures are
// conservatively unknown unless an authoritative response proves otherwise.
type Step struct {
	Stage     string  `json:"stage"`
	Attempted bool    `json:"attempted"`
	Outcome   Outcome `json:"outcome"`
}

// Mutation describes the requested message action, separately from preparation
// and attachment uploads. Steps retain side effects even if the action never ran.
type Mutation struct {
	Action          string  `json:"action"`
	ChatID          string  `json:"chat_id,omitempty"`
	MessageID       string  `json:"message_id,omitempty"`
	RequestSequence *int64  `json:"request_sequence,omitempty"`
	Attempted       bool    `json:"attempted"`
	Outcome         Outcome `json:"outcome"`
	Steps           []Step  `json:"steps,omitempty"`
}

// Diagnostic is the allowlisted request failure detail in the machine channel.
type Diagnostic struct {
	Kind          string `json:"kind"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	GatewayCode   *int   `json:"gateway_code,omitempty"`
	Exception     string `json:"exception,omitempty"`
	ExceptionCode *int   `json:"exception_code,omitempty"`
}

type Error struct {
	Code       string      `json:"code"`
	Stage      string      `json:"stage"`
	Message    string      `json:"message"`
	Mutation   *Mutation   `json:"mutation,omitempty"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
	cause      error
}

func New(code, stage, message string, cause error) *Error {
	return &Error{Code: code, Stage: stage, Message: message, cause: cause}
}
func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }
