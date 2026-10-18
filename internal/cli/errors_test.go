package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const outcomeChat = "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func machineDetail(t *testing.T, err error) *fault.Error {
	t.Helper()
	var machine *MachineError
	if !errors.As(err, &machine) {
		t.Fatalf("missing machine error: %v", err)
	}
	return machine.Detail
}
func TestJSONErrorArgumentsStayLocalAndRedacted(t *testing.T) {
	for _, args := range [][]string{
		{"send", outcomeChat, "--text", "secret-message", "--unknown=secret-token"},
		{"send"}, {"messages", outcomeChat, "--limit", "secret-token"},
		{"send", outcomeChat, "--text", "--error-format=json", "--bad"},
		{"login"}, {"watch"},
	} {
		a, out, diagnostics := testApp(nil)
		a.Interactive = true
		a.Lock = func() (func(), error) { t.Fatal("validation accessed storage"); return nil, nil }
		err := a.Run(append([]string{"--error-format=json"}, args...))
		detail := machineDetail(t, err)
		body, _ := json.Marshal(detail)
		if detail.Code != "invalid_arguments" || out.Len() != 0 || diagnostics.Len() != 0 || strings.Contains(string(body), "secret-") {
			t.Fatalf("bad diagnostics: %s", body)
		}
	}
}
func TestJSONErrorFlagDoesNotConsumeMessageText(t *testing.T) {
	a, _, _ := testApp(nil)
	f := &messageAPI{}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"--error-format", "json", "send", outcomeChat, "--allow-plaintext", "--text", "--error-format=json", "--json"}); err != nil {
		t.Fatal(err)
	}
	if f.sent.Text != "--error-format=json" {
		t.Fatal("changed text")
	}
}
func TestJSONPartialHistoryKeepsArray(t *testing.T) {
	a, out, diagnostics := testApp(nil)
	f := &messageAPI{history: []*line.Message{{ID: "9007199254740993", Text: "hello"}, {ID: "9007199254740994", ContentMetadata: map[string]string{"e2eeVersion": "2"}}}}
	a.Manager.NewClient = func(string) session.API { return f }
	err := a.Run([]string{"--error-format=json", "messages", outcomeChat, "--json"})
	detail := machineDetail(t, err)
	var messages []messaging.Message
	if json.Unmarshal(out.Bytes(), &messages) != nil || len(messages) != 2 || messages[0].ID != "9007199254740993" || detail.Code != "decryption_failed" || detail.Stage != "decode" || diagnostics.Len() != 0 {
		t.Fatalf("bad partial result: %+v", detail)
	}
}

type failedWriter struct{ err error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.err }
func TestConfirmedSendOutputFailureRetainsReceipt(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		a, _, _ := testApp(nil)
		f := &messageAPI{}
		a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		a.Manager.NewClient = func(string) session.API { return f }
		a.Out = failedWriter{errors.New("secret writer details")}
		args := []string{"--error-format=json", "send", outcomeChat, "--allow-plaintext", "--text", "synthetic"}
		if jsonOutput {
			args = append(args, "--json")
		}
		err := a.Run(args)
		detail := machineDetail(t, err)
		if detail.Code != "output_failed" || detail.Mutation == nil || detail.Mutation.Outcome != fault.Confirmed || detail.Mutation.MessageID != "message-id" || detail.Mutation.RequestSequence == nil {
			t.Fatalf("lost receipt: %+v", detail)
		}
		body, _ := json.Marshal(detail)
		if strings.Contains(string(body), "secret writer") {
			t.Fatal("leaked output error")
		}
	}
}
func TestStructuredClassificationAndWrapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		exit int
	}{
		{session.ErrNotFound, "no_session", 1}, {session.ErrSessionInvalidated, "invalid_session", 1},
		{session.ErrBusy, "busy", 75}, {session.ErrStorageFormat, "storage_invalid_format", 65},
		{session.ErrDurabilityUncertain, "storage_durability_uncertain", 74},
		{context.Canceled, "cancelled", 1}, {context.DeadlineExceeded, "deadline_exceeded", 1},
		{&line.ResponseError{Status: 401}, "authentication_failed", 1},
		{fault.New("unsupported_content", "lookup", "Unsupported content.", nil), "unsupported_content", 1},
	} {
		err := fmt.Errorf("secret wrapping: %w", tc.err)
		detail := DescribeError(err, "preparation", nil)
		body, _ := json.Marshal(detail)
		if detail.Code != tc.code || strings.Contains(string(body), "secret") || session.StorageExitCode(err) != tc.exit {
			t.Fatalf("wrong classification: %s", body)
		}
	}
}
func TestJSONErrorHelpAndAppReuse(t *testing.T) {
	a, _, diagnostics := testApp(nil)
	if err := a.Run([]string{"--error-format=json", "send", "--help"}); err != nil || !strings.Contains(diagnostics.String(), "Usage:") {
		t.Fatal("missing help", err)
	}
	diagnostics.Reset()
	if err := a.Run([]string{"--error-format=json", "send"}); err == nil {
		t.Fatal("missing failure")
	}
	if err := a.Run([]string{"version"}); err != nil || a.JSONErrors {
		t.Fatal("state leaked", err)
	}
	if _, err := (&errorWriter{Writer: failedWriter{io.ErrClosedPipe}}).Write(nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("lost output cause")
	}
}

func TestKnownIDsSurviveLockFailure(t *testing.T) {
	a, _, _ := testApp(nil)
	a.Lock = func() (func(), error) { return nil, session.ErrBusy }
	err := a.Run([]string{"--error-format=json", "unsend", outcomeChat, "--message", "9007199254740993"})
	detail := machineDetail(t, err)
	if detail.Mutation == nil || detail.Mutation.ChatID != outcomeChat || detail.Mutation.MessageID != "9007199254740993" || detail.Mutation.Outcome != fault.NotAttempted {
		t.Fatalf("lost known IDs: %+v", detail)
	}
}

func TestSignalAfterSuccessRetainsConfirmedReceipt(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &messageAPI{}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"--error-format=json", "send", outcomeChat, "--allow-plaintext", "--text", "synthetic", "--json"}); err != nil {
		t.Fatal(err)
	}
	detail := machineDetail(t, a.SignalError())
	if out.Len() == 0 || detail.Code != "cancelled" || detail.Mutation.Outcome != fault.Confirmed || detail.Mutation.MessageID != "message-id" {
		t.Fatal("signal hid successful receipt")
	}
}

type transientOutputWriter struct {
	firstErr error
	short    bool
	calls    int
}

func (w *transientOutputWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		if w.short {
			return len(p) - 1, nil
		}
		return 0, w.firstErr
	}
	return len(p), nil
}

func TestJSONErrorsRetainIgnoredFirstOutputFailure(t *testing.T) {
	for _, short := range []bool{false, true} {
		a, _, diagnostics := testApp(nil)
		a.Manager.Store = &statusTestStore{status: session.StorageStatus{Email: "synthetic@example.test"}}
		first := errors.New("secret output details")
		if short {
			first = io.ErrShortWrite
		}
		writer := &transientOutputWriter{firstErr: first, short: short}
		a.Out = writer
		err := a.Run([]string{"--error-format=json", "auth", "status"})
		detail := machineDetail(t, err)
		if writer.calls < 2 || detail.Code != "output_failed" || detail.Stage != "output" || !errors.Is(err, first) || diagnostics.Len() != 0 {
			t.Fatalf("ignored output failure: calls=%d error=%v", writer.calls, err)
		}
		body, _ := json.Marshal(detail)
		if strings.Contains(string(body), "secret output details") {
			t.Fatal("leaked output cause")
		}
		if err := a.Run([]string{"--error-format=json", "version"}); err != nil {
			t.Fatal("retained error leaked to next run", err)
		}
	}
}

func TestJSONErrorsKeepOperationFailureWhenOutputAlsoFailed(t *testing.T) {
	a, _, _ := testApp(nil)
	a.Manager.Store = &statusTestStore{err: session.ErrStorageFormat}
	a.Out = &transientOutputWriter{firstErr: io.ErrClosedPipe}
	err := a.Run([]string{"--error-format=json", "auth", "status"})
	detail := machineDetail(t, err)
	if detail.Code != "storage_invalid_format" || !errors.Is(err, session.ErrStorageFormat) || session.StorageExitCode(err) != 65 {
		t.Fatalf("output error hid operation failure: %v", err)
	}
}
