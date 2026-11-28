package line

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsRefreshRequired(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "talk exception code 119",
			err:  errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":119,"reason":"Access token refresh required"}}`),
			want: true,
		},
		{
			name: "refresh text",
			err:  errors.New("Access token refresh required"),
			want: true,
		},
		{
			name: "refresh text lower case",
			err:  errors.New("access token refresh required"),
			want: true,
		},
		{
			name: "other talk exception",
			err:  errors.New(`API error 400: {"code":10051,"data":{"name":"TalkException","code":10,"reason":"not a member"}}`),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRefreshRequired(tt.err); got != tt.want {
				t.Fatalf("IsRefreshRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLoggedOut(t *testing.T) {
	if !IsLoggedOut(errors.New("V3_TOKEN_CLIENT_LOGGED_OUT")) {
		t.Fatal("expected logged-out error to be detected")
	}
	if !IsLoggedOut(errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":83,"reason":"invalid sender key","parameterMap":null}}`)) {
		t.Fatal("expected invalid sender key to be detected as logged out")
	}
	if !IsLoggedOut(errors.New(`SSE error: 401: {"code":10004,"message":"REQUEST_NEED_LOGIN"}`)) {
		t.Fatal("expected request-need-login to be detected as logged out")
	}
	if !IsLoggedOut(errors.New(`SSE error: 401: {"code":10004}`)) {
		t.Fatal("expected request-need-login code to be detected as logged out")
	}
	if IsLoggedOut(errors.New(`SSE error: 401: {"code":100040}`)) {
		t.Fatal("similar longer numeric code should not be classified as logged out")
	}
	if IsLoggedOut(errors.New("Access token refresh required")) {
		t.Fatal("refresh-required error should not be classified as logged out")
	}
}

func TestIsUnauthorizedStatus(t *testing.T) {
	for _, err := range []error{
		errors.New("API error 401: unauthorized"),
		errors.New("API error 403: forbidden"),
		errors.New("HTTP 401: unauthorized"),
		errors.New("HTTP 403: forbidden"),
		errors.New("SSE error: 401"),
		errors.New("SSE error: 403"),
		errors.New("OBS upload failed (401): unauthorized"),
		errors.New("OBS upload failed (403): forbidden"),
		errors.New("OBS object info failed (401): unauthorized"),
		errors.New("OBS object info failed (403): forbidden"),
		errors.New("OBS download failed (401): unauthorized"),
		errors.New("OBS download failed (403): forbidden"),
		errors.New("api error 401: unauthorized"),
		errors.New("http 403: forbidden"),
		errors.New("sse ERROR: 401"),
		errors.New("obs DOWNLOAD failed (403): forbidden"),
	} {
		if !IsUnauthorizedStatus(err) {
			t.Fatalf("expected %q to be unauthorized", err)
		}
	}

	if IsUnauthorizedStatus(errors.New("HTTP 404: not found")) {
		t.Fatal("404 should not be classified as unauthorized")
	}
	for _, err := range []error{
		errors.New("request failed: dial tcp: i/o timeout"),
		errors.New("OBS upload request failed: connection reset by peer"),
		errors.New("OBS download request failed: context deadline exceeded"),
	} {
		if IsUnauthorizedStatus(err) {
			t.Fatalf("network error %q should not be classified as unauthorized", err)
		}
	}
}

func TestIsTalkExceptionNotFound(t *testing.T) {
	err := errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":5,"reason":"not found","parameterMap":null}}`)
	if !IsTalkExceptionNotFound(err) {
		t.Fatal("expected TalkException code 5 not found to be detected")
	}
	if IsTalkExceptionNotFound(errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":10,"reason":"not a member","parameterMap":null}}`)) {
		t.Fatal("not-a-member should not be classified as not-found")
	}
	if IsTalkExceptionNotFound(errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":5,"reason":"different","parameterMap":null}}`)) {
		t.Fatal("code 5 with a different reason should not be classified as not-found")
	}
	if IsTalkExceptionNotFound(nil) {
		t.Fatal("nil should not be classified as not-found")
	}
}

func TestIsNoUsableE2EEGroupKeyExceedMaxMember(t *testing.T) {
	err := errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":100,"reason":"exceed max member","parameterMap":{}}}`)
	if !IsNoUsableE2EEGroupKey(err) {
		t.Fatal("expected exceed max member to disable group E2EE")
	}

	otherCode100 := errors.New(`API error 400: {"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","message":"TalkException","code":100,"reason":"different reason","parameterMap":{}}}`)
	if IsNoUsableE2EEGroupKey(otherCode100) {
		t.Fatal("unrelated TalkException code 100 must not disable group E2EE")
	}

	phraseOutsideReason := errors.New(`API error 400: {"code":10051,"message":"exceed max member","data":{"name":"TalkException","code":100,"reason":"different reason"}}`)
	if IsNoUsableE2EEGroupKey(phraseOutsideReason) {
		t.Fatal("exceed max member outside the reason field must not disable group E2EE")
	}
}

func TestNoUsableE2EEGroupKeyTalkExceptionExceedMaxMember(t *testing.T) {
	if !isNoUsableE2EEGroupKeyTalkException("RESPONSE_ERROR", talkExceptionData{
		Name:   "TalkException",
		Code:   100,
		Reason: "exceed max member",
	}) {
		t.Fatal("expected structured exceed max member response to disable group E2EE")
	}
}

func TestLogoutReasonClassificationAndRedaction(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"non-logout", errors.New("network timeout"), ""},
		{"v3 logged out", responseError(401, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"V3_TOKEN_CLIENT_LOGGED_OUT"}}`)), "V3_TOKEN_CLIENT_LOGGED_OUT"},
		{"invalid sender key", responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":83,"reason":"invalid sender key"}}`)), "INVALID_SENDER_KEY"},
		{"request need login", responseError(401, []byte(`{"code":10004,"message":"REQUEST_NEED_LOGIN"}`)), "REQUEST_NEED_LOGIN"},
		{"talk exception 7 not available user", responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":7,"reason":"secret freeform"}}`)), "NOT_AVAILABLE_USER"},
		{"talk exception 7 safe enum", responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":7,"reason":"NOT_AVAILABLE_USER"}}`)), "NOT_AVAILABLE_USER"},
		{"talk exception 8 safe enum", responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"MUST_UPGRADE"}}`)), "MUST_UPGRADE"},
		{"talk exception 8 freeform redacted", responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":8,"reason":"secret freeform token"}}`)), "AUTHENTICATION_FAILED"},
		{"token auth exception 3 redacted", responseError(401, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TokenAuthException","code":3,"reason":"secret body"}}`)), "TOKEN_AUTH_LOGGED_OUT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LogoutReason(tc.err); got != tc.want {
				t.Fatalf("LogoutReason() = %q, want %q", got, tc.want)
			}
		})
	}
	if !IsLoggedOut(responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":7,"reason":"deactivated"}}`))) {
		t.Fatal("expected TalkException 7 to classify as IsLoggedOut")
	}
}

func TestIsE2EEGroupMemberMismatchAndReceiverKeyRejected(t *testing.T) {
	mismatch := responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":99,"reason":"invalid member count"}}`))
	if !IsE2EEGroupMemberMismatch(mismatch) {
		t.Fatal("expected TalkException 99 to match IsE2EEGroupMemberMismatch")
	}
	if IsE2EEGroupMemberMismatch(responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":98,"reason":"member settings off"}}`))) {
		t.Fatal("code 98 must not match IsE2EEGroupMemberMismatch")
	}

	for _, body := range []string{
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":83,"reason":"invalid receiver key"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":84,"reason":"E2EE_UPDATE_RECEIVER_KEY"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":86,"reason":"E2EE_INVALID_VERSION"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":87,"reason":"E2EE_SENDER_DISABLED"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":88,"reason":"E2EE_RECEIVER_DISABLED"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":90,"reason":"E2EE_RECEIVER_NOT_ALLOWED"}}`,
	} {
		if !IsE2EEReceiverKeyRejected(responseError(400, []byte(body))) {
			t.Fatalf("expected IsE2EEReceiverKeyRejected for HTTP 400 %s", body)
		}
		if !IsE2EEReceiverKeyRejected(responseError(200, []byte(body))) {
			t.Fatalf("expected IsE2EEReceiverKeyRejected for HTTP 200 %s", body)
		}
		if IsE2EEReceiverKeyRejected(responseError(503, []byte(body))) {
			t.Fatalf("HTTP 503 must not match IsE2EEReceiverKeyRejected for %s", body)
		}
	}

	// Speculative codes 81/82 and "invalid sender key" must NOT match receiver-key rejection.
	for _, body := range []string{
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":81,"reason":"E2EE_RETRY_ENCRYPT"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":82,"reason":"E2EE_RETRY_PLAIN"}}`,
		`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":83,"reason":"invalid sender key"}}`,
	} {
		if IsE2EEReceiverKeyRejected(responseError(400, []byte(body))) {
			t.Fatalf("must not match IsE2EEReceiverKeyRejected for %s", body)
		}
	}
}

func TestIsTransientErrorTalkExceptionCodes(t *testing.T) {
	for _, code := range []int{2, 4, 15, 20, 33} {
		body := []byte(fmt.Sprintf(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":%d,"reason":"transient"}}`, code))
		if !IsTransientError(responseError(200, body)) || !IsTransientError(responseError(400, body)) {
			t.Fatalf("expected TalkException %d to classify as transient", code)
		}
	}
	// Non-transient TalkException and refresh rejections stay non-transient.
	if IsTransientError(responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":5,"reason":"not found"}}`))) {
		t.Fatal("TalkException 5 must not be transient")
	}
	rejected := refreshFailure(responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":20,"reason":"internal"}}`)))
	if IsTransientError(rejected) {
		t.Fatal("refresh rejection must not be transient")
	}
}

func TestIsGroupKeyNotFoundRequiresAuthoritativeStatusAndEnvelope(t *testing.T) {
	valid := []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":5,"reason":"not found"}}`)
	if !IsGroupKeyNotFound(responseError(200, valid)) || !IsGroupKeyNotFound(responseError(400, valid)) {
		t.Fatal("expected HTTP 200/400 TalkException 5 to match IsGroupKeyNotFound")
	}
	for _, status := range []int{404, 500, 502, 503} {
		if IsGroupKeyNotFound(responseError(status, valid)) {
			t.Fatalf("HTTP %d must not match IsGroupKeyNotFound", status)
		}
	}
	wrongEnvelope := responseError(400, []byte(`{"code":10052,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":5,"reason":"not found"}}`))
	if IsGroupKeyNotFound(wrongEnvelope) {
		t.Fatal("non-10051 envelope must not match IsGroupKeyNotFound")
	}
	wrongNamespace := responseError(400, []byte(`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"OtherException","code":5,"reason":"not found"}}`))
	if IsGroupKeyNotFound(wrongNamespace) {
		t.Fatal("non-TalkException must not match IsGroupKeyNotFound")
	}
}
