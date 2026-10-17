package line

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type failingResponseBody struct{}

func (failingResponseBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingResponseBody) Close() error             { return nil }

func TestSendRequestDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
		cause            error
	}{
		{name: "transport", kind: "transport", cause: errors.New("private-transport")},
		{name: "cancelled", kind: "transport", cause: context.Canceled},
		{name: "response_read", kind: "response_read", status: 200},
		{name: "http", body: "private-body", kind: "gateway", status: 502},
		{name: "gateway", body: `{"code":10051,"data":{"name":"TalkException","code":5,"reason":"private-body"}}`, kind: "gateway", status: 200},
		{name: "invalid_json", body: "private-body", kind: "response_envelope", status: 200},
		{name: "missing_code", body: `{"data":{"id":"123"}}`, kind: "response_envelope", status: 200},
		{name: "decode", body: `{"code":0,"data":{"id":123,"text":"private-body"}}`, kind: "message_decode", status: 200},
		{name: "null_receipt", body: `{"code":0,"data":null}`, kind: "missing_receipt", status: 200},
		{name: "empty_receipt", body: `{"code":0,"data":{}}`, kind: "missing_receipt", status: 200},
		{name: "success", body: `{"code":0,"data":{"id":"9007199254740993"}}`, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewClient("synthetic")
			c.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.cause != nil {
					return nil, tc.cause
				}
				response := qrTestResponse(tc.status, tc.body)
				if tc.name == "response_read" {
					response.Body = failingResponseBody{}
				}
				return response, nil
			})}
			sent, err := c.SendMessageContext(context.Background(), 1, &Message{To: "c-test", Text: "synthetic"})
			if calls != 1 {
				t.Fatalf("send calls: %d", calls)
			}
			if tc.kind == "" {
				if err != nil || sent == nil || sent.ID != "9007199254740993" {
					t.Fatal("lost successful receipt")
				}
				return
			}
			d := RequestDiagnostics(err)
			if err == nil || d == nil || d.Kind != tc.kind {
				t.Fatalf("diagnostic: %+v; error: %v", d, err)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatal("lost original cause")
			}
			encoded, _ := json.Marshal(d)
			if strings.Contains(string(encoded)+err.Error(), "private-") {
				t.Fatal("diagnostics leaked data")
			}
			if tc.name == "gateway" && (d.HTTPStatus != 200 || d.GatewayCode == nil || *d.GatewayCode != 10051 || d.Exception != "TalkException" || d.ExceptionCode == nil || *d.ExceptionCode != 5) {
				t.Fatalf("missing safe codes: %+v", d)
			}
		})
	}
}

func TestRequestDiagnosticsOnlyExposeKnownExceptionNames(t *testing.T) {
	err := responseError(400, []byte(`{"code":10051,"data":{"name":"private-name","code":123}}`))
	d := RequestDiagnostics(err)
	if d.Exception != "" || d.ExceptionCode != nil || d.GatewayCode == nil || *d.GatewayCode != 10051 {
		t.Fatalf("unexpected diagnostic: %+v", d)
	}
	if RequestDiagnostics(errors.New("HTTP 400 private-body")) != nil {
		t.Fatal("classified arbitrary error text")
	}
}
