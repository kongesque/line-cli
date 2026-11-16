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

func TestRefreshProtocolAndPublishAfterPersistence(t *testing.T) {
	c := NewClient("old-access")
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var input RefreshAccessTokenRequest
		if json.Unmarshal(body, &input) != nil || input.RefreshToken != "current-refresh" || input.RetryCount != 2 {
			t.Fatal("incorrect refresh arguments")
		}
		if req.Method != "POST" || req.URL.String() != "https://line-chrome-gw.line-apps.com/api/auth/tokenRefresh" || req.Header.Get("x-line-access") != "old-access" || req.Header.Get("Cookie") != "lct=old-access" || req.Header.Get("x-hmac") == "" {
			t.Fatal("incorrect Chrome refresh request")
		}
		return qrTestResponse(200, `{"accessToken":"new-access","refreshToken":"new-refresh","durationUntilRefreshInSec":"604800","tokenIssueTimeEpochSec":"1000","refreshApiRetryPolicy":{"initialDelayInMillis":"1000","maxDelayInMillis":"10000","multiplier":2,"jitterRate":0.1}}`), nil
	})
	token, err := c.RefreshAccessTokenContext(context.Background(), "current-refresh", 2)
	if err != nil || token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" || token.TokenIssueTimeEpochSec != "1000" || token.DurationUntilRefreshSec != "604800" || token.RefreshApiRetryPolicy == nil || token.RefreshApiRetryPolicy.MaxDelayInMillis != "10000" {
		t.Fatal("refresh response lost metadata", err)
	}
	if c.AccessToken != "old-access" {
		t.Fatal("refresh published credentials before owner could save them")
	}
}

// Issue #29: the gateway may wrap the token in its {code, message, data}
// envelope. Dropping that reply after the server rotated the refresh token
// stranded the session, so both shapes must yield the same token.
func TestRefreshAcceptsGatewayEnvelopeAndBareToken(t *testing.T) {
	token := `{"accessToken":"new-access","refreshToken":"new-refresh","durationUntilRefreshInSec":"302400","tokenIssueTimeEpochSec":"1000","refreshApiRetryPolicy":{"initialDelayInMillis":"1000","maxDelayInMillis":"10000","multiplier":2,"jitterRate":0.1}}`
	for name, body := range map[string]string{
		"wrapped":           `{"code":0,"message":"OK","data":` + token + `}`,
		"wrapped no code":   `{"data":` + token + `}`,
		"bare":              token,
		"bare with null":    strings.TrimSuffix(token, "}") + `,"data":null}`,
		"wrapped with ws":   "{\n\"code\" : 0,\n\"data\" :\n" + token + "\n}",
		"bare with code ok": strings.TrimSuffix(token, "}") + `,"code":0}`,
	} {
		c := NewClient("old-access")
		c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return qrTestResponse(200, body), nil })
		got, err := c.RefreshAccessTokenContext(context.Background(), "old-refresh", 0)
		if err != nil || got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" || got.DurationUntilRefreshSec != "302400" || got.TokenIssueTimeEpochSec != "1000" || got.RefreshApiRetryPolicy == nil || got.RefreshApiRetryPolicy.MaxDelayInMillis != "10000" {
			t.Fatalf("%s refresh reply lost token data: %+v %v", name, got, err)
		}
		if c.AccessToken != "old-access" {
			t.Fatalf("%s refresh published credentials before owner could save them", name)
		}
	}
}

func TestRefreshMalformedSuccessIsEnvelopeFailure(t *testing.T) {
	for _, body := range []string{
		`{"secret":"never-print"}`,
		`{"code":0,"message":"OK","data":{"secret":"never-print"}}`,
		`{"code":0,"data":"never-print"}`,
		`{"code":0,"data":{"accessToken":17}}`,
		`never-print`,
		``,
	} {
		c := NewClient("old")
		c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return qrTestResponse(200, body), nil })
		_, err := c.RefreshAccessTokenContext(context.Background(), "refresh", 0)
		d := RequestDiagnostics(err)
		if err == nil || strings.Contains(err.Error(), "never-print") || IsAuthError(err) || IsRefreshRejected(err) || IsTransientError(err) || d == nil || d.Kind != "response_envelope" || c.AccessToken != "old" {
			t.Fatalf("malformed refresh reply %q misclassified: %v %+v", body, err, d)
		}
	}
}

func TestRefreshResponseClassificationAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		status                            int
		body                              string
		auth, logout, transient, rejected bool
	}{
		{200, `{"code":10051,"data":{"name":"TalkException","code":119},"secret":"never-print"}`, true, false, false, true},
		{401, `{"secret":"never-print"}`, true, false, false, true},
		{403, `{"secret":"never-print"}`, true, false, false, true},
		{400, `{"code":10051,"data":{"name":"TokenAuthException","code":1},"secret":"never-print"}`, true, false, false, true},
		{400, `{"code":10051,"data":{"name":"TalkException","code":8},"secret":"never-print"}`, true, true, false, true},
		// Issue #29: LINE's answer to a refresh token it no longer accepts.
		{400, `{"code":10201,"message":"never-print"}`, true, false, false, true},
		{400, `{"secret":"never-print"}`, true, false, false, true},
		{200, `{"code":10201,"message":"never-print"}`, true, false, false, true},
		{404, `{"secret":"never-print"}`, false, false, false, false},
		{408, `{"secret":"never-print"}`, false, false, true, false},
		{429, `{"secret":"never-print"}`, false, false, true, false},
		{503, `{"secret":"never-print"}`, false, false, true, false},
		{200, `{"secret":"never-print"}`, false, false, false, false},
	} {
		c := NewClient("old")
		c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return qrTestResponse(tc.status, tc.body), nil })
		_, err := c.RefreshAccessToken("refresh")
		if err == nil || strings.Contains(err.Error(), "never-print") || IsAuthError(err) != tc.auth || IsLoggedOut(err) != tc.logout || IsTransientError(err) != tc.transient || IsRefreshRejected(err) != tc.rejected || c.AccessToken != "old" {
			t.Fatalf("incorrect response classification for status %d %s: %v", tc.status, tc.body, err)
		}
	}
}

func TestRefreshRejectionKeepsGatewayDiagnostic(t *testing.T) {
	c := NewClient("old")
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return qrTestResponse(400, `{"code":10201,"message":"never-print"}`), nil
	})
	_, err := c.RefreshAccessTokenContext(context.Background(), "refresh", 0)
	d := RequestDiagnostics(err)
	if d == nil || d.Kind != "gateway" || d.HTTPStatus != 400 || d.GatewayCode == nil || *d.GatewayCode != 10201 || d.Exception != "" {
		t.Fatalf("refresh rejection lost its diagnostic: %+v", d)
	}
}

func TestRefreshRejectionIsScopedToTokenRefresh(t *testing.T) {
	// The same gateway reply from another endpoint is not a refresh rejection.
	c := NewClient("old")
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return qrTestResponse(400, `{"code":10201,"message":"never-print"}`), nil
	})
	_, err := c.GetProfileContext(context.Background())
	if err == nil || IsRefreshRejected(err) || IsAuthError(err) {
		t.Fatal("non-refresh gateway error classified as a refresh rejection", err)
	}
}

func TestRPCPreservesHTTP200AuthEnvelopes(t *testing.T) {
	c := NewClient("old")
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return qrTestResponse(200, "{\n\"code\" : 10051, \"data\": {\"name\":\"TalkException\", \"code\" : 119}, \"secret\":\"never-print\"}"), nil
	})
	_, err := c.GetProfile()
	if !IsRefreshRequired(err) || IsLoggedOut(err) || strings.Contains(err.Error(), "never-print") {
		t.Fatal("HTTP 200 auth envelope lost", err)
	}
}

func TestRefreshCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient("old")
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() })
	_, err := c.RefreshAccessTokenContext(ctx, "refresh", 0)
	if !errors.Is(err, context.Canceled) || IsTransientError(err) {
		t.Fatal("cancelled refresh treated as retryable", err)
	}
}

func TestRefreshCodeBoundariesAndUnrelatedFailures(t *testing.T) {
	for _, body := range []string{`{"code":1190}`, `{"code":11900}`, `{"code":10051}`, `{"code":10051,"data":{"name":"TalkException","code":10}}`} {
		if IsAuthError(errors.New(body)) {
			t.Fatal("unrelated failure classified as authentication")
		}
	}
	if !IsRefreshRequired(errors.New("{\n\"code\" \t: 119\n}")) {
		t.Fatal("whitespace prevented refresh detection")
	}
}

func TestHTTPClientTimeoutCanRetryRefresh(t *testing.T) {
	// http.Client's own timeout wraps DeadlineExceeded, even while the command
	// context is still active. Manager checks command cancellation separately.
	if !IsTransientError(context.DeadlineExceeded) {
		t.Fatal("transport timeout was not retryable")
	}
}
