package line

// QR login protocol client (PLAN.md phase 1).
//
// Static protocol evidence: official LINE Chrome Extension 3.7.2, inspected
// read-only from the LY Corporation Chrome Web Store package. Manifest
// version 3.7.2; static/js/main.js SHA-256
// 2912a06d868c2829636be1613c622f28807efe74a7868cfb143678a38b80cc2a;
// static/js/ltsmSandbox.js SHA-256
// 5c66cba945cc039d817e87fef2aa1e28c8adb7be0f94fca5b95bb6bcf5a07c94.
// These are static source observations, not live protocol validation.
//
// Concrete source anchors in main.js (minified identifiers):
//   - SD(e,t) builds POST calls: (config, ...args) => POST `/api/${e}` with
//     the argument array as the JSON body. QR endpoints:
//     talk/thrift/LoginQrCode/SecondaryQrCodeLoginService/{createSession,
//     createQrCode, verifyCertificate, createPinCode, qrCodeLoginV2} and
//     talk/thrift/LoginQrCode/SecondaryQrCodeLoginPermitNoticeService/
//     {checkQrCodeVerified, checkPinCodeVerified}.
//   - QrCodeLoginV2 request: {systemName:"CHROMEOS", modelName:"CHROME",
//     autoLoginIsRequired:false, authSessionId} wrapped in a one-element
//     array; response destructured as {certificate, metaData,
//     tokenV3IssueResult}.
//   - Scan poll: checkQrCodeVerified with {authSessionId}, headers
//     {"X-Line-Session-ID": session, "X-LST": longPollingIntervalSec*Dd}
//     where Dd=1e3, and retryCount=longPollingMaxCount-1 with
//     retryCondition Ez.
//   - Ez(e) is exactly 410 === pM(e)?.statusCode, where pM returns
//     e.response.data.data only when the gateway body code is
//     RESPONSE_HTTP_ERROR=10052. Never conflate this nested status with an
//     outer HTTP 410 or a service error code.
//   - Gateway retry interceptor: initial delay UD=Dd (1s), next delay
//     Math.min(2*delay, 3e4) (doubling, capped at 30s).
//   - PIN poll: checkPinCodeVerified with {authSessionId}, headers
//     {"X-Line-Session-ID": session, "X-LST": rH} where rH=11e4 (110000ms);
//     createPinCode returns {pinCode}.
//   - verifyCertificate posts {authSessionId, certificate}; Chrome catches
//     every rejection and falls back to PIN. Rejection codes are unknown, so
//     this client propagates verify errors without any PIN-fallback
//     classification.
//   - QR metadata is inspected for errorCode absent/SUCCESS plus keyId
//     before unwrapping keys. Absence of keyId is not evidence of an
//     explicit LSOFF capability, and no LSOFF metadata shape is evidenced.
//
// Unknowns requiring live validation (do not invent): the outer HTTP status
// and wire envelope for scan polling, exact invalid/missing-certificate
// error codes, LSON/LSOFF metaData shapes, and the displacement boundary of
// qrCodeLoginV2.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	gen "github.com/kongesque/line-cli/pkg"
)

const (
	qrGatewayBase             = "https://line-chrome-gw.line-apps.com"
	qrLoginServiceRoot        = "talk/thrift/LoginQrCode/SecondaryQrCodeLoginService"
	qrPermitNoticeServiceRoot = "talk/thrift/LoginQrCode/SecondaryQrCodeLoginPermitNoticeService"

	gatewayResponseHTTPError = 10052
	qrPinLongPollWait        = 110 * time.Second
	qrRetryInitialDelay      = time.Second
	qrRetryMaxDelay          = 30 * time.Second

	// CLI policy bounds, not server protocol constants.
	qrLongPollMargin     = 30 * time.Second
	qrMaxBodyBytes       = 1 << 20
	qrMaxPollIntervalSec = 3600
	qrMaxPollCount       = 1000
)

var (
	// ErrQRCodeExpired means all scan polls for one QR session returned the
	// evidenced wrapped 410. Only this error permits a fresh QR attempt.
	ErrQRCodeExpired = errors.New("QR code expired before scan")
	// ErrQRCertificateRejected is reserved for evidenced invalid/missing certificate
	// responses. No server code is mapped yet; unknown failures propagate.
	ErrQRCertificateRejected = errors.New("QR certificate rejected")
	ErrQRPinTimeout          = errors.New("PIN approval timed out")
)

// QRServiceError retains numeric classification without retaining server
// messages, exception names, URLs, or raw response bodies. No certificate
// rejection code is classified until evidence establishes one.
type QRServiceError struct {
	Method      string
	HTTPStatus  int
	Code        int
	StatusCode  int
	ServiceCode *int
}

func (e *QRServiceError) Error() string {
	return fmt.Sprintf("QR %s failed (HTTP %d, gateway code %d)", e.Method, e.HTTPStatus, e.Code)
}

// QRCompleteError marks possible dispatch, including a lost response. A true
// Dispatched value is conservative: it does not assert remote approval.
// Callers must never replay final login automatically.
type QRCompleteError struct {
	Dispatched bool
	// Approved means a successful response contained an access token, but
	// required encryption metadata could not be used. No local session was saved.
	Approved bool
	err      error
}

func (e *QRCompleteError) Error() string {
	if e.Approved {
		return fmt.Sprintf("QR login approved but encryption setup is incomplete: %v", e.err)
	}
	if e.Dispatched {
		return fmt.Sprintf("QR final login may have completed remotely: %v", e.err)
	}
	return fmt.Sprintf("QR final login was not dispatched: %v", e.err)
}

func (e *QRCompleteError) Unwrap() error { return e.err }

// QRChallenge contains sensitive, ephemeral login data. Orchestration appends
// the public key, owns its lifetime, and must not log or persist this value.
type QRChallenge struct {
	AuthSessionID       string
	CallbackURL         string
	LongPollIntervalSec int
	LongPollMaxCount    int
}

type qrSessionRequest struct {
	AuthSessionID string `json:"authSessionId"`
}

type qrCertificateRequest struct {
	AuthSessionID string `json:"authSessionId"`
	Certificate   string `json:"certificate"`
}

type qrCompleteRequest struct {
	SystemName          string `json:"systemName"`
	ModelName           string `json:"modelName"`
	AutoLoginIsRequired bool   `json:"autoLoginIsRequired"`
	AuthSessionID       string `json:"authSessionId"`
}

type qrCodeResponse struct {
	CallbackURL         string `json:"callbackUrl"`
	LongPollIntervalSec int    `json:"longPollingIntervalSec"`
	LongPollMaxCount    int    `json:"longPollingMaxCount"`
}

type qrPinResponse struct {
	PinCode string `json:"pinCode"`
}

// qrMetaData describes QR Letter Sealing metadata. Missing keys or unknown
// error codes never imply that Letter Sealing is disabled.
type qrMetaData struct {
	ErrorCode         qrMetadataCode `json:"errorCode"`
	KeyID             string         `json:"keyId"`
	PublicKey         string         `json:"publicKey"`
	EncryptedKeyChain string         `json:"encryptedKeyChain"`
	E2EEVersion       string         `json:"e2eeVersion"`
}

// A missing code is accepted by Chrome; explicit empty/null codes are not.
type qrMetadataCode struct {
	present bool
	value   string
}

func (c *qrMetadataCode) UnmarshalJSON(data []byte) error {
	c.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invalid QR metadata code")
	}
	return json.Unmarshal(data, &c.value)
}

type qrLoginV2Response struct {
	Certificate        string              `json:"certificate"`
	Mid                string              `json:"mid"`
	MetaData           *qrMetaData         `json:"metaData"`
	TokenV3IssueResult *TokenV3IssueResult `json:"tokenV3IssueResult"`
}

// BeginQRLogin creates one fresh server session and retrieves its challenge.
// It does not create the login curve key or regenerate expired challenges.
func (c *Client) BeginQRLogin(ctx context.Context) (*QRChallenge, error) {
	var session qrSessionRequest
	if err := c.qrCall(ctx, qrLoginServiceRoot, "createSession", struct{}{}, "", 0, &session); err != nil {
		return nil, err
	}
	if session.AuthSessionID == "" {
		return nil, errors.New("QR createSession returned no session ID")
	}
	var qr qrCodeResponse
	if err := c.qrCall(ctx, qrLoginServiceRoot, "createQrCode", session, "", 0, &qr); err != nil {
		return nil, err
	}
	u, err := url.Parse(qr.CallbackURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return nil, errors.New("QR createQrCode returned an invalid callback URL")
	}
	if err := validateQRPollParams(qr.LongPollIntervalSec, qr.LongPollMaxCount); err != nil {
		return nil, err
	}
	return &QRChallenge{session.AuthSessionID, qr.CallbackURL, qr.LongPollIntervalSec, qr.LongPollMaxCount}, nil
}

// WaitForQRScan repeats only recognized wrapped-410 scan polls, on the same
// session, with Chrome's bounded backoff. Other failures never signal expiry.
func (c *Client) WaitForQRScan(ctx context.Context, challenge *QRChallenge) error {
	return c.waitForQRScan(ctx, challenge, qrSleep)
}

// A per-call sleeper keeps retry tests deterministic without global overrides.
func (c *Client) waitForQRScan(ctx context.Context, challenge *QRChallenge, sleep func(context.Context, time.Duration) error) error {
	if challenge == nil || challenge.AuthSessionID == "" {
		return errors.New("QR scan requires a session ID")
	}
	if err := validateQRPollParams(challenge.LongPollIntervalSec, challenge.LongPollMaxCount); err != nil {
		return err
	}
	// Bounds were checked before duration conversion, avoiding overflow.
	wait := time.Duration(challenge.LongPollIntervalSec) * time.Second
	delay := qrRetryInitialDelay
	for attempt := 0; attempt < challenge.LongPollMaxCount; attempt++ {
		err := c.qrCall(ctx, qrPermitNoticeServiceRoot, "checkQrCodeVerified", qrSessionRequest{challenge.AuthSessionID}, challenge.AuthSessionID, wait, nil)
		if err == nil || !isQRWrappedExpiry(err) {
			return err
		}
		if attempt+1 == challenge.LongPollMaxCount {
			return ErrQRCodeExpired
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		delay = min(delay*2, qrRetryMaxDelay)
	}
	return ErrQRCodeExpired
}

// VerifyQRCertificate submits even an empty certificate, as Chrome does on a
// first login. All failures propagate; no speculative PIN fallback is applied.
func (c *Client) VerifyQRCertificate(ctx context.Context, sessionID, certificate string) error {
	if sessionID == "" {
		return errors.New("QR certificate verification requires a session ID")
	}
	return c.qrCall(ctx, qrLoginServiceRoot, "verifyCertificate", qrCertificateRequest{sessionID, certificate}, "", 0, nil)
}

// CreateQRPin returns the server's PIN verbatim, including any leading zeros.
func (c *Client) CreateQRPin(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("QR PIN creation requires a session ID")
	}
	var pin qrPinResponse
	if err := c.qrCall(ctx, qrLoginServiceRoot, "createPinCode", qrSessionRequest{sessionID}, "", 0, &pin); err != nil {
		return "", err
	}
	if pin.PinCode == "" {
		return "", errors.New("QR createPinCode returned no PIN")
	}
	return pin.PinCode, nil
}

// WaitForQRPin makes one 110-second permit-notice poll. PIN timeout must never
// regenerate a QR code: the user may still be looking at phone approval.
func (c *Client) WaitForQRPin(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("QR PIN approval requires a session ID")
	}
	err := c.qrCall(ctx, qrPermitNoticeServiceRoot, "checkPinCodeVerified", qrSessionRequest{sessionID}, sessionID, qrPinLongPollWait, nil)
	if isQRWrappedExpiry(err) {
		return ErrQRPinTimeout
	}
	return err
}

// CompleteQRLogin attempts final authentication once per invocation. It does
// not mutate Client.AccessToken or save a session. Orchestration must validate
// the profile, refresh data and exported keys before saving the result.
func (c *Client) CompleteQRLogin(ctx context.Context, sessionID string) (*LoginResult, error) {
	approved := false
	fail := func(dispatched bool, err error) (*LoginResult, error) {
		return nil, &QRCompleteError{Dispatched: dispatched, Approved: approved, err: err}
	}
	if sessionID == "" {
		return fail(false, errors.New("QR final login requires a session ID"))
	}
	args := qrCompleteRequest{"CHROMEOS", "CHROME", false, sessionID}
	body, dispatched, err := c.qrRequest(ctx, qrLoginServiceRoot, "qrCodeLoginV2", args, "", 0)
	if err != nil {
		return fail(dispatched, err)
	}
	var completion qrLoginV2Response
	if err := parseQREnvelope("qrCodeLoginV2", http.StatusOK, body, &completion); err != nil {
		return fail(dispatched, err)
	}
	if completion.TokenV3IssueResult == nil || completion.TokenV3IssueResult.AccessToken == "" {
		return fail(dispatched, errors.New("QR final login returned no access token"))
	}
	approved = true
	metadata := completion.MetaData
	if metadata == nil || (metadata.ErrorCode.present && metadata.ErrorCode.value != "SUCCESS") {
		return fail(dispatched, errors.New("QR final login returned unavailable Letter Sealing metadata"))
	}
	publicKey, pubErr := base64.StdEncoding.DecodeString(metadata.PublicKey)
	keyChain, chainErr := base64.StdEncoding.DecodeString(metadata.EncryptedKeyChain)
	// Existing LoginUnwrapKeyChain also accepts a prefixed public key and
	// normalizes its trailing 32 bytes; preserve that behavior here.
	if pubErr != nil || len(publicKey) < 32 || chainErr != nil || len(keyChain) == 0 || metadata.KeyID == "" {
		return fail(dispatched, errors.New("QR final login returned incomplete Letter Sealing keys"))
	}
	return &LoginResult{
		Certificate: completion.Certificate, Mid: completion.Mid,
		TokenV3IssueResult: completion.TokenV3IssueResult,
		AuthToken:          completion.TokenV3IssueResult.AccessToken,
		EncryptedKeyChain:  metadata.EncryptedKeyChain, E2EEPublicKey: metadata.PublicKey,
		E2EEVersion: metadata.E2EEVersion, E2EEKeyID: metadata.KeyID,
	}, nil
}

func (c *Client) qrCall(ctx context.Context, serviceRoot, method string, arg any, sessionID string, wait time.Duration, out any) error {
	body, _, err := c.qrRequest(ctx, serviceRoot, method, arg, sessionID, wait)
	if err != nil {
		return err
	}
	return parseQREnvelope(method, http.StatusOK, body, out)
}

// qrRequest returns whether dispatch may have occurred. All messages are local
// constants; transport/reader errors can contain credentials and are discarded
// except for cancellation/deadline identity. The gateway host is fixed and tests
// inject Client.HTTPClient.Transport instead of a production URL override.
func (c *Client) qrRequest(ctx context.Context, serviceRoot, method string, arg any, sessionID string, wait time.Duration) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("QR request requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	path := "/api/" + serviceRoot + "/" + method
	bodyBytes, err := json.Marshal([]any{arg})
	if err != nil {
		return nil, false, errors.New("could not encode QR request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qrGatewayBase+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, false, errors.New("could not construct QR request")
	}
	// Never make these POSTs replayable, even if a transport retries requests.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("x-line-chrome-version", ExtensionVersion)
	req.Header.Set("x-line-application", lineApplicationHeader)
	req.Header.Set("x-lal", "en_US")
	if sessionID != "" {
		req.Header.Set("X-Line-Session-ID", sessionID)
	}
	if wait > 0 {
		req.Header.Set("X-LST", strconv.FormatInt(wait.Milliseconds(), 10))
	}
	if c.AccessToken != "" {
		req.Header.Set("x-line-access", c.AccessToken)
		req.Header.Set("Cookie", "lct="+c.AccessToken)
	}
	runner, err := gen.GetRunner()
	if err != nil {
		return nil, false, errors.New("could not initialize QR request signer")
	}
	signature, err := runner.GetSignature(path, string(bodyBytes), c.AccessToken)
	if err != nil {
		return nil, false, errors.New("could not sign QR request")
	}
	req.Header.Set("x-hmac", signature)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	resp, err := c.qrHTTPClient(wait).Do(req)
	if err != nil {
		return nil, true, qrIOError(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, qrMaxBodyBytes+1))
	if err != nil {
		return nil, true, qrIOError(ctx, err)
	}
	if len(body) > qrMaxBodyBytes {
		return nil, true, errors.New("QR response exceeds size limit")
	}
	if resp.StatusCode != http.StatusOK {
		// Gateway failures may use non-200 outer statuses. Only the validated
		// gateway envelope carries a protocol classification, never status alone.
		var serviceErr *QRServiceError
		if err := parseQREnvelope(method, resp.StatusCode, body, nil); errors.As(err, &serviceErr) {
			return nil, true, serviceErr
		}
		return nil, true, &QRServiceError{Method: method, HTTPStatus: resp.StatusCode}
	}
	return body, true, nil
}

func qrIOError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("QR request or response transport failed")
}

func (c *Client) qrHTTPClient(wait time.Duration) *http.Client {
	client := http.Client{Timeout: rpcClientTimeout}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	if wait > 0 {
		client.Timeout = wait + qrLongPollMargin
	} else if client.Timeout <= 0 {
		client.Timeout = rpcClientTimeout
	}
	// A redirect can replay a final POST or leak signed session headers to a
	// different endpoint. Cookies are supplied explicitly, not taken from a jar.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	return &client
}

func parseQREnvelope(method string, status int, body []byte, out any) error {
	var env struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil || env.Code == nil {
		return errors.New("invalid QR response envelope")
	}
	if *env.Code != 0 {
		e := &QRServiceError{Method: method, HTTPStatus: status, Code: *env.Code}
		var data struct {
			StatusCode int  `json:"statusCode"`
			Code       *int `json:"code"`
		}
		if json.Unmarshal(env.Data, &data) == nil {
			e.ServiceCode = data.Code
			if e.Code == gatewayResponseHTTPError {
				e.StatusCode = data.StatusCode
			}
		}
		return e
	}
	// Void responses may be null or an object, but a missing data field is not
	// a successful approval. Typed results must be non-null objects.
	data := bytes.TrimSpace(env.Data)
	if len(data) == 0 || (data[0] != '{' && !bytes.Equal(data, []byte("null"))) {
		return errors.New("invalid QR response data")
	}
	if out != nil && (bytes.Equal(data, []byte("null")) || json.Unmarshal(data, out) != nil) {
		return errors.New("invalid QR response data")
	}
	return nil
}

func isQRWrappedExpiry(err error) bool {
	var serviceErr *QRServiceError
	return errors.As(err, &serviceErr) && serviceErr.Code == gatewayResponseHTTPError && serviceErr.StatusCode == http.StatusGone
}

func validateQRPollParams(intervalSec, maxCount int) error {
	if intervalSec <= 0 || intervalSec > qrMaxPollIntervalSec || maxCount <= 0 || maxCount > qrMaxPollCount {
		return errors.New("QR polling parameters are missing or outside supported bounds")
	}
	return nil
}

func qrSleep(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
