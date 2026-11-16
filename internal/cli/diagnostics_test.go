package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type diagnosticSendAPI struct {
	messageAPI
	client *line.Client
}

func (f *diagnosticSendAPI) SendMessageContext(ctx context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	return f.client.SendMessageContext(ctx, seq, msg)
}

func TestSendDiagnosticSurvivesSessionAndMachineWrapping(t *testing.T) {
	for _, tc := range []struct{ body, kind string }{
		{`{"code":10051,"data":{"name":"TalkException","code":82,"reason":"private-reason"}}`, "gateway"},
		{`{"code":0,"data":{"id":123,"text":"private-text"}}`, "message_decode"},
		{`{"code":0,"data":null}`, "missing_receipt"},
	} {
		a, out, _ := testApp(nil)
		calls := 0
		client := line.NewClient("synthetic")
		client.HTTPClient = &http.Client{Transport: diagnosticTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		f := &diagnosticSendAPI{client: client}
		store := &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		a.Manager.Store = store
		a.Manager.NewClient = func(string) session.API { return f }
		err := a.Run([]string{"--error-format=json", "send", "id:c-group", "--text", "synthetic", "--json"})
		d := machineDetail(t, err)
		if d.Code != "remote_failed" || d.Stage != "send" || d.Diagnostic == nil || d.Diagnostic.Kind != tc.kind || d.Mutation == nil || d.Mutation.Outcome != fault.Unknown || !d.Mutation.Attempted || calls != 1 || out.Len() != 0 {
			t.Fatalf("incorrect send result: %+v; calls %d", d, calls)
		}
		if d.Mutation.RequestSequence == nil || *d.Mutation.RequestSequence != store.state.LastReqSeq {
			t.Fatal("lost persisted sequence")
		}
		if tc.kind == "gateway" && (d.Diagnostic.ExceptionCode == nil || *d.Diagnostic.ExceptionCode != 82) {
			t.Fatal("lost E2EE_RETRY_ENCRYPT diagnostic")
		}
		encoded, _ := json.Marshal(d)
		if strings.Contains(string(encoded), "private-") {
			t.Fatal("machine channel leaked data")
		}
	}
}

func TestHistoryDiagnosticsIDsBeforeDecryption(t *testing.T) {
	a, out, _ := testApp(nil)
	f := &messageAPI{history: []*line.Message{
		{ID: "9007199254740993", Text: "private-text", Chunks: []string{"private-chunk"}, ContentMetadata: map[string]string{"e2eeVersion": "2"}},
		nil, {ID: "private-invalid-id"}, {ID: "9007199254740994", RelatedMessageID: "123"},
	}}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	if err := a.Run([]string{"messages", "id:c-group", "--diagnostics", "--json", "--limit", "50"}); err != nil {
		t.Fatal(err)
	}
	var d messaging.HistoryDiagnostics
	if json.Unmarshal(out.Bytes(), &d) != nil || d.SchemaVersion != 1 || d.Method != "getRecentMessagesV2" || d.RequestedCount != 50 || d.ReturnedCount != 4 || d.NullCount != 1 || d.InvalidIDCount != 1 || len(d.MessageIDs) != 2 || d.MessageIDs[0] != "9007199254740993" || d.MessageIDs[1] != "9007199254740994" {
		t.Fatalf("diagnostics: %s", out.String())
	}
	if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "chunks") || strings.Contains(out.String(), "text") {
		t.Fatal("history diagnostics leaked message data")
	}
}

func TestHistoryDiagnosticFlagsValidateBeforeSession(t *testing.T) {
	for _, extra := range [][]string{{}, {"--json", "--details"}, {"--json", "--page"}, {"--json", "--cursor", "bad"}, {"--json", "--resolve-names"}} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("invalid flags accessed session"); return nil, nil }
		if err := a.Run(append([]string{"messages", "id:c-group", "--diagnostics"}, extra...)); err == nil {
			t.Fatalf("accepted invalid flags: %v", extra)
		}
	}
}

// rejectingRefreshClient is a real client whose token refresh gets issue #29's
// reply: HTTP 400 with gateway code 10201.
func rejectingRefreshClient(paths *[]string) func(string) session.API {
	return func(token string) session.API {
		c := line.NewClient(token)
		c.HTTPClient.Transport = diagnosticTransport(func(r *http.Request) (*http.Response, error) {
			*paths = append(*paths, r.URL.Path)
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":10201,"message":"private-reason"}`)), Request: r}, nil
		})
		return c
	}
}

// Issue #29 reported "check your connection" and remote_failed for a refresh
// that LINE had rejected; a new login is the only way forward.
func TestRejectedRefreshAsksForLogin(t *testing.T) {
	for _, jsonErrors := range []bool{false, true} {
		a, out, _ := testApp(nil)
		var paths []string
		a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", RefreshToken: "synthetic-refresh", RefreshAt: time.Unix(1, 0), NoE2EE: true}}
		a.Manager.NewClient = rejectingRefreshClient(&paths)
		args := []string{"whoami"}
		if jsonErrors {
			args = append([]string{"--error-format=json"}, args...)
		}
		err := a.Run(args)
		if err == nil || len(paths) != 1 || paths[0] != "/api/auth/tokenRefresh" || out.Len() != 0 {
			t.Fatalf("json=%v: unexpected requests %v or output: %v", jsonErrors, paths, err)
		}
		if !jsonErrors {
			if !strings.Contains(err.Error(), "LINE rejected the token refresh; run line login") || strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "private-") {
				t.Fatalf("human error lacks login guidance: %v", err)
			}
			continue
		}
		d := machineDetail(t, err)
		if d.Code != "authentication_failed" || !strings.Contains(d.Message, "run line login") || d.Diagnostic == nil || d.Diagnostic.Kind != "gateway" || d.Diagnostic.HTTPStatus != 400 || d.Diagnostic.GatewayCode == nil || *d.Diagnostic.GatewayCode != 10201 {
			t.Fatalf("machine error: %+v %+v", d, d.Diagnostic)
		}
		if encoded, _ := json.Marshal(d); strings.Contains(string(encoded), "private-") {
			t.Fatal("machine channel leaked data")
		}
	}
}
