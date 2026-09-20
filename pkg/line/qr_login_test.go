package line

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	gen "github.com/kongesque/line-cli/pkg"
)

const qrTestSession = "synthetic-session"
const qrTestExpiry = `{"code":10052,"data":{"statusCode":410}}`

func qrTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func qrTestClient(fn func(*http.Request) (*http.Response, error)) *Client {
	c := NewClient("")
	c.HTTPClient.Transport = roundTripFunc(fn)
	return c
}

func qrTestCompletion() map[string]any {
	return map[string]any{
		"certificate": "synthetic-certificate", "mid": "synthetic-mid",
		"tokenV3IssueResult": map[string]any{
			"accessToken": "synthetic-access", "refreshToken": "synthetic-refresh",
			"durationUntilRefreshInSec": "3600", "tokenIssueTimeEpochSec": "1234",
		},
		"metaData": map[string]any{
			"errorCode": "SUCCESS", "keyId": "123", "e2eeVersion": "1",
			"publicKey":         base64.StdEncoding.EncodeToString(make([]byte, 32)),
			"encryptedKeyChain": base64.StdEncoding.EncodeToString([]byte("synthetic-ciphertext")),
		},
	}
}

func qrTestEnvelope(t *testing.T, data any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"code": 0, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestQRProtocolRequestsAndCompletion(t *testing.T) {
	const loginRoot = "/api/talk/thrift/LoginQrCode/SecondaryQrCodeLoginService/"
	const pollRoot = "/api/talk/thrift/LoginQrCode/SecondaryQrCodeLoginPermitNoticeService/"
	steps := []struct {
		path, body, session, wait, response string
	}{
		{loginRoot + "createSession", `[{}]`, "", "", `{"code":0,"data":{"authSessionId":"synthetic-session"}}`},
		{loginRoot + "createQrCode", `[{"authSessionId":"synthetic-session"}]`, "", "", `{"code":0,"data":{"callbackUrl":"line://au/q/synthetic","longPollingIntervalSec":60,"longPollingMaxCount":3}}`},
		{pollRoot + "checkQrCodeVerified", `[{"authSessionId":"synthetic-session"}]`, qrTestSession, "60000", `{"code":0,"data":{}}`},
		{loginRoot + "verifyCertificate", `[{"authSessionId":"synthetic-session","certificate":"synthetic-cert"}]`, "", "", `{"code":0,"data":null}`},
		{loginRoot + "createPinCode", `[{"authSessionId":"synthetic-session"}]`, "", "", `{"code":0,"data":{"pinCode":"001234"}}`},
		{pollRoot + "checkPinCodeVerified", `[{"authSessionId":"synthetic-session"}]`, qrTestSession, "110000", `{"code":0,"data":null}`},
		{loginRoot + "qrCodeLoginV2", `[{"systemName":"CHROMEOS","modelName":"CHROME","autoLoginIsRequired":false,"authSessionId":"synthetic-session"}]`, "", "", qrTestEnvelope(t, qrTestCompletion())},
	}
	calls := 0
	c := qrTestClient(func(req *http.Request) (*http.Response, error) {
		if calls >= len(steps) {
			t.Fatal("unexpected extra request")
		}
		step := steps[calls]
		calls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "line-chrome-gw.line-apps.com" || req.URL.Path != step.path || string(body) != step.body {
			t.Fatalf("unexpected request %d: %s %s %s", calls, req.Method, req.URL, body)
		}
		for key, want := range map[string]string{
			"Content-Type": "application/json", "User-Agent": UserAgent,
			"X-Line-Chrome-Version": "3.7.2", "X-Line-Application": "CHROMEOS\t3.7.2\tChrome_OS\t",
			"X-LAL": "en_US", "X-Line-Session-ID": step.session, "X-LST": step.wait,
			"X-Line-Access": "", "Cookie": "",
		} {
			if got := req.Header.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		runner, err := gen.GetRunner()
		if err != nil {
			t.Fatal(err)
		}
		signature, err := runner.GetSignature(step.path, step.body, "")
		if err != nil || req.Header.Get("X-Hmac") != signature {
			t.Fatal("request signature did not match the exact path/body")
		}
		if req.GetBody != nil {
			t.Fatal("authentication POST is replayable")
		}
		return qrTestResponse(200, step.response), nil
	})
	ctx := context.Background()
	challenge, err := c.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.CallbackURL != "line://au/q/synthetic" || challenge.AuthSessionID != qrTestSession || challenge.LongPollIntervalSec != 60 || challenge.LongPollMaxCount != 3 {
		t.Fatal("challenge fields lost")
	}
	if err := c.WaitForQRScan(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	if err := c.VerifyQRCertificate(ctx, qrTestSession, "synthetic-cert"); err != nil {
		t.Fatal(err)
	}
	if pin, err := c.CreateQRPin(ctx, qrTestSession); err != nil || pin != "001234" {
		t.Fatalf("PIN not preserved: %q, %v", pin, err)
	}
	if err := c.WaitForQRPin(ctx, qrTestSession); err != nil {
		t.Fatal(err)
	}
	result, err := c.CompleteQRLogin(ctx, qrTestSession)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthToken != "synthetic-access" || result.TokenV3IssueResult.RefreshToken != "synthetic-refresh" || result.TokenV3IssueResult.DurationUntilRefreshSec != "3600" || result.Certificate != "synthetic-certificate" || result.Mid != "synthetic-mid" || result.E2EEKeyID != "123" || result.E2EEPublicKey == "" || result.EncryptedKeyChain == "" || result.E2EEVersion != "1" || result.NoE2EE {
		t.Fatal("completion lost fields or silently downgraded encryption")
	}
	if calls != len(steps) || c.AccessToken != "" {
		t.Fatal("incorrect request count or client authentication changed")
	}
}

func TestQRScanRetriesSameSessionWithBoundedBackoff(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(map[bool]string{false: "eventual_scan", true: "exhausted"}[exhausted], func(t *testing.T) {
			calls := 0
			var delays []time.Duration
			c := qrTestClient(func(req *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(req.Body)
				if !strings.HasSuffix(req.URL.Path, "/checkQrCodeVerified") || req.Header.Get("X-Line-Session-ID") != qrTestSession || string(body) != `[{"authSessionId":"synthetic-session"}]` {
					t.Fatal("retry changed session or called a mutation")
				}
				if calls == 8 && !exhausted {
					return qrTestResponse(200, `{"code":0,"data":null}`), nil
				}
				return qrTestResponse(400, qrTestExpiry), nil
			})
			err := c.waitForQRScan(context.Background(), &QRChallenge{AuthSessionID: qrTestSession, LongPollIntervalSec: 1, LongPollMaxCount: 8}, func(_ context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return nil
			})
			if errors.Is(err, ErrQRCodeExpired) != exhausted || (!exhausted && err != nil) || calls != 8 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
			if !reflect.DeepEqual(delays, want) {
				t.Fatalf("delays=%v", delays)
			}
		})
	}
}

func TestQRScanDoesNotRetryOtherErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"outer_410":       {410, `not a gateway response`},
		"service_410":     {400, `{"code":10051,"data":{"code":410,"statusCode":410}}`},
		"nested_500":      {500, `{"code":10052,"data":{"statusCode":500}}`},
		"spoofed_message": {200, `{"code":10052,"message":"wrapped status 410","data":{}}`},
		"bad_nested_type": {200, `{"code":10052,"data":{"statusCode":"410"}}`},
		"missing_code":    {200, `{"data":{}}`},
		"missing_data":    {200, `{"code":0}`},
		"null_envelope":   {200, `null`},
		"server_error":    {500, `{"code":0,"data":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c := qrTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				return qrTestResponse(tc.status, tc.body), nil
			})
			err := c.waitForQRScan(context.Background(), &QRChallenge{AuthSessionID: qrTestSession, LongPollIntervalSec: 1, LongPollMaxCount: 3}, func(context.Context, time.Duration) error {
				t.Fatal("unexpected retry")
				return nil
			})
			if err == nil || errors.Is(err, ErrQRCodeExpired) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestQRPinTimeoutAndCertificateErrors(t *testing.T) {
	calls := 0
	c := qrTestClient(func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(req.URL.Path, "/checkPinCodeVerified") {
			return qrTestResponse(400, qrTestExpiry), nil
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != `[{"authSessionId":"synthetic-session","certificate":""}]` {
			t.Fatal("empty certificate was not submitted as Chrome does")
		}
		// Synthetic error code for passthrough, not a claimed certificate code.
		return qrTestResponse(400, `{"code":10051,"message":"private-content","data":{"code":777,"reason":"private-content"}}`), nil
	})
	if err := c.WaitForQRPin(context.Background(), qrTestSession); !errors.Is(err, ErrQRPinTimeout) || errors.Is(err, ErrQRCodeExpired) {
		t.Fatalf("incorrect PIN classification: %v", err)
	}
	err := c.VerifyQRCertificate(context.Background(), qrTestSession, "")
	var serviceErr *QRServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != 10051 || serviceErr.ServiceCode == nil || *serviceErr.ServiceCode != 777 || calls != 2 || strings.Contains(err.Error(), "private-content") {
		t.Fatalf("certificate error was lost, leaked or retried: %v", err)
	}
}

func TestQRLongPollTimeoutAndClientIsolation(t *testing.T) {
	c := NewClient("")
	original := c.HTTPClient
	for _, wait := range []time.Duration{60 * time.Second, qrPinLongPollWait} {
		clone := c.qrHTTPClient(wait)
		if clone == original || clone.Timeout != wait+30*time.Second || original.Timeout != 30*time.Second {
			t.Fatal("long poll shortened or shared client mutated")
		}
	}
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) < 130*time.Second || req.Header.Get("X-LST") != "110000" {
			t.Fatal("PIN polling did not get its full server wait")
		}
		return qrTestResponse(200, `{"code":0,"data":null}`), nil
	})
	if err := c.WaitForQRPin(context.Background(), qrTestSession); err != nil {
		t.Fatal(err)
	}
}

func TestQRCancellation(t *testing.T) {
	for _, method := range []string{"scan", "pin", "complete"} {
		t.Run(method, func(t *testing.T) {
			testRPCContextCancellation(t, method, func(c *Client, ctx context.Context) error {
				switch method {
				case "scan":
					return c.WaitForQRScan(ctx, &QRChallenge{AuthSessionID: qrTestSession, LongPollIntervalSec: 60, LongPollMaxCount: 3})
				case "pin":
					return c.WaitForQRPin(ctx, qrTestSession)
				default:
					_, err := c.CompleteQRLogin(ctx, qrTestSession)
					var completionErr *QRCompleteError
					if !errors.As(err, &completionErr) || !completionErr.Dispatched {
						t.Error("cancellation lost possible final dispatch")
					}
					return err
				}
			})
		})
	}
	t.Run("backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		c := qrTestClient(func(*http.Request) (*http.Response, error) {
			calls++
			cancel()
			return qrTestResponse(200, qrTestExpiry), nil
		})
		start := time.Now()
		err := c.WaitForQRScan(ctx, &QRChallenge{AuthSessionID: qrTestSession, LongPollIntervalSec: 1, LongPollMaxCount: 3})
		if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) > time.Second {
			t.Fatalf("backoff failed cancellation: calls=%d err=%v", calls, err)
		}
	})
}

func TestQRFinalLoginNeverReplayedOrRedirected(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			c := qrTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				resp := qrTestResponse(status, `{"code":10052,"data":{"statusCode":410}}`)
				resp.Header.Set("Location", "https://example.invalid/private-value")
				return resp, nil
			})
			_, err := c.CompleteQRLogin(context.Background(), qrTestSession)
			var completionErr *QRCompleteError
			if !errors.As(err, &completionErr) || !completionErr.Dispatched || calls != 1 || errors.Is(err, ErrQRCodeExpired) {
				t.Fatalf("final login replayed or misclassified: calls=%d err=%v", calls, err)
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("redirect URL leaked")
			}
		})
	}
}

func TestQRFinalLoginDispatchAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport bool
		body      string
	}{
		{"transport", true, ""},
		{"malformed", false, "private-value"},
		{"server_message", false, `{"code":10006,"message":"private-value","data":{"reason":"private-value"}}`},
		{"missing_result", false, `{"code":0,"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := qrTestClient(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transport {
					return nil, errors.New("private-value")
				}
				return qrTestResponse(200, tc.body), nil
			})
			_, err := c.CompleteQRLogin(context.Background(), qrTestSession)
			var completionErr *QRCompleteError
			if !errors.As(err, &completionErr) || !completionErr.Dispatched || calls != 1 || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("unsafe final error: calls=%d err=%v", calls, err)
			}
		})
	}
	c := qrTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input reached transport")
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx     context.Context
		session string
	}{
		{ctx, qrTestSession}, {nil, qrTestSession}, {context.Background(), ""},
	} {
		_, err := c.CompleteQRLogin(tc.ctx, tc.session)
		var completionErr *QRCompleteError
		if !errors.As(err, &completionErr) || completionErr.Dispatched {
			t.Fatalf("pre-dispatch failure misclassified: %v", err)
		}
	}
}

func TestQRCompletionFailsClosed(t *testing.T) {
	for _, field := range []string{"metaData", "errorCode", "publicKey", "encryptedKeyChain", "keyId", "tokenV3IssueResult", "accessToken"} {
		t.Run(field, func(t *testing.T) {
			data := qrTestCompletion()
			switch field {
			case "metaData", "tokenV3IssueResult":
				data[field] = nil
			case "accessToken":
				data["tokenV3IssueResult"].(map[string]any)[field] = ""
			case "errorCode":
				data["metaData"].(map[string]any)[field] = "UNKNOWN_SYNTHETIC_ERROR"
			default:
				data["metaData"].(map[string]any)[field] = ""
			}
			body := qrTestEnvelope(t, data)
			c := qrTestClient(func(*http.Request) (*http.Response, error) { return qrTestResponse(200, body), nil })
			result, err := c.CompleteQRLogin(context.Background(), qrTestSession)
			if err == nil || result != nil || c.AccessToken != "" {
				t.Fatal("malformed completion accepted or authentication changed")
			}
		})
	}
}

func TestQRBeginRejectsMalformedResponses(t *testing.T) {
	for _, data := range []any{
		nil, map[string]any{},
		map[string]any{"callbackUrl": "not-a-url", "longPollingIntervalSec": 1, "longPollingMaxCount": 1},
		map[string]any{"callbackUrl": "line://au/q/synthetic", "longPollingIntervalSec": 0, "longPollingMaxCount": 1},
		map[string]any{"callbackUrl": "line://au/q/synthetic", "longPollingIntervalSec": 1, "longPollingMaxCount": -1},
		map[string]any{"callbackUrl": "line://au/q/synthetic", "longPollingIntervalSec": math.MaxInt64, "longPollingMaxCount": 1},
	} {
		c := qrTestClient(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/createSession") {
				return qrTestResponse(200, `{"code":0,"data":{"authSessionId":"synthetic-session"}}`), nil
			}
			return qrTestResponse(200, qrTestEnvelope(t, data)), nil
		})
		if _, err := c.BeginQRLogin(context.Background()); err == nil {
			t.Fatal("malformed challenge accepted")
		}
	}
}

func TestQRInvalidPollParametersDoNotContactServer(t *testing.T) {
	c := qrTestClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid challenge reached transport")
		return nil, nil
	})
	for _, challenge := range []*QRChallenge{
		nil, {}, {AuthSessionID: qrTestSession},
		{AuthSessionID: qrTestSession, LongPollIntervalSec: math.MaxInt, LongPollMaxCount: 1},
		{AuthSessionID: qrTestSession, LongPollIntervalSec: 1, LongPollMaxCount: math.MaxInt},
		{AuthSessionID: qrTestSession, LongPollIntervalSec: -1, LongPollMaxCount: 1},
	} {
		if err := c.WaitForQRScan(context.Background(), challenge); err == nil {
			t.Fatal("invalid polling parameters accepted")
		}
	}
}

type qrTrackingBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *qrTrackingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *qrTrackingBody) Close() error { b.closed = true; return nil }

type qrBrokenReader struct{}

func (qrBrokenReader) Read([]byte) (int, error) { return 0, errors.New("private-value") }

func TestQRResponseBoundsAndReadFailures(t *testing.T) {
	for _, reader := range []io.Reader{strings.NewReader(strings.Repeat("private-value", qrMaxBodyBytes)), qrBrokenReader{}} {
		body := &qrTrackingBody{reader: reader}
		c := qrTestClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		})
		_, err := c.CompleteQRLogin(context.Background(), qrTestSession)
		var completionErr *QRCompleteError
		if !errors.As(err, &completionErr) || !completionErr.Dispatched || strings.Contains(err.Error(), "private-value") || body.read > qrMaxBodyBytes+1 || !body.closed {
			t.Fatalf("unsafe response handling: bytes=%d closed=%v err=%v", body.read, body.closed, err)
		}
	}
}

func TestQRMetadataErrorCodePresence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		present     bool
		code        any
		wantSuccess bool
	}{
		{"absent", false, nil, true},
		{"success", true, "SUCCESS", true},
		{"null", true, nil, false},
		{"empty", true, "", false},
		{"wrong_type", true, 0, false},
		{"unknown", true, "UNKNOWN_SYNTHETIC_CODE", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := qrTestCompletion()
			metadata := data["metaData"].(map[string]any)
			delete(metadata, "errorCode")
			if tc.present {
				metadata["errorCode"] = tc.code
			}
			body := qrTestEnvelope(t, data)
			c := qrTestClient(func(*http.Request) (*http.Response, error) { return qrTestResponse(200, body), nil })
			result, err := c.CompleteQRLogin(context.Background(), qrTestSession)
			if (err == nil) != tc.wantSuccess || (result != nil && result.NoE2EE) {
				t.Fatalf("unexpected metadata classification: %v", err)
			}
			if tc.name == "unknown" || tc.name == "empty" {
				var completionErr *QRCompleteError
				if !errors.As(err, &completionErr) || !completionErr.Approved || !completionErr.Dispatched {
					t.Fatal("confirmed approval lost on metadata failure")
				}
			}
		})
	}
}
