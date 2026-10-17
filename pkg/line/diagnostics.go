package line

import (
	"encoding/json"
	"errors"
)

// RequestDiagnostic contains only fixed categories and numeric protocol codes.
// It deliberately excludes server messages, bodies, headers, and field values.
type RequestDiagnostic struct {
	Kind          string `json:"kind"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	GatewayCode   *int   `json:"gateway_code,omitempty"`
	Exception     string `json:"exception,omitempty"`
	ExceptionCode *int   `json:"exception_code,omitempty"`
}

type diagnosticError struct {
	kind  string
	cause error
}

func (e *diagnosticError) Error() string { return "LINE request failed (" + e.kind + ")" }
func (e *diagnosticError) Unwrap() error { return e.cause }

func requestFailure(kind string, cause error) error {
	return &diagnosticError{kind: kind, cause: cause}
}

// ErrMissingMessageReceipt means success could not be established. It is not
// evidence that the message was rejected, and must never trigger a retry.
var ErrMissingMessageReceipt = errors.New("LINE returned no message receipt")

// RequestDiagnostics inspects typed errors only, never arbitrary error text.
// Causes remain wrapped so authentication, cancellation, and outcome handling
// keep their existing behavior.
func RequestDiagnostics(err error) *RequestDiagnostic {
	if errors.Is(err, ErrMissingMessageReceipt) {
		return &RequestDiagnostic{Kind: "missing_receipt"}
	}
	var diagnostic *diagnosticError
	if errors.As(err, &diagnostic) {
		return &RequestDiagnostic{Kind: diagnostic.kind}
	}
	var response *ResponseError
	if !errors.As(err, &response) {
		return nil
	}
	d := &RequestDiagnostic{Kind: "gateway", HTTPStatus: response.Status}
	var envelope struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(response.body), &envelope) == nil {
		d.GatewayCode = envelope.Code
		var exception struct {
			Name string `json:"name"`
			Code *int   `json:"code"`
		}
		if json.Unmarshal(envelope.Data, &exception) != nil {
			return d
		}
		switch exception.Name {
		case "TalkException", "TokenAuthException":
			d.Exception, d.ExceptionCode = exception.Name, exception.Code
		}
	}
	return d
}
