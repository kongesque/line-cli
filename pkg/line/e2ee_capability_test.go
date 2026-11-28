package line

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestExplicitPeerCapabilityDoesNotConflateMalformedKeys(t *testing.T) {
	for _, tc := range []struct {
		body     string
		disabled bool
	}{
		{`{"specVersion":-1,"allowedTypes":[]}`, true},
		{`{"allowedTypes":[]}`, true},
		{`{}`, false},
		{`{"publicKey":"!not-base64!","keyId":42}`, false},
		{`{"publicKey":null,"keyId":42}`, false},
	} {
		_, err := parseE2EEPublicKey([]byte(tc.body))
		if err == nil || IsE2EEDisabled(err) != tc.disabled {
			t.Fatalf("incorrect classification for %s", tc.body)
		}
		if !errors.Is(err, ErrNoUsableE2EEPublicKey) {
			t.Fatal("legacy error identity lost")
		}
	}
}

func TestExplicitGroupCapabilityExcludesAuthAndMissingKey(t *testing.T) {
	for _, tc := range []struct {
		body     string
		disabled bool
	}{
		{`HTTP 400: {"code":10051,"data":{"name":"TalkException","code":98,"reason":"member settings off"}}`, true},
		{`HTTP 400: {"code":10051,"data":{"name":"TalkException","code":100,"reason":"exceed max member"}}`, true},
		{`HTTP 400: {"code":10051,"data":{"name":"TalkException","code":1,"reason":"auth failed"}}`, false},
		{`HTTP 400: {"code":10051,"data":{"name":"TalkException","code":5,"reason":"not found"}}`, false},
		{`HTTP 400: {"code":10051,"data":{"name":"TalkException","code":100,"reason":"other failure"}}`, false},
		{`no group key found`, false},
	} {
		if IsE2EEDisabled(errors.New(tc.body)) != tc.disabled {
			t.Fatalf("incorrect classification: %s", tc.body)
		}
	}
	err := parseE2EEGroupKeyError("test", "RESPONSE_ERROR", []byte(`{"name":"TalkException","code":98,"reason":"member settings off"}`))
	if !IsE2EEDisabled(err) || !errors.Is(err, ErrNoUsableE2EEGroupKey) {
		t.Fatal("typed response lost capability/legacy identity")
	}
}

func TestGetLastE2EEPublicKeysSingleArgAndErrors(t *testing.T) {
	var gotPath, gotBody string
	c := NewClient("token")
	c.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		b, _ := io.ReadAll(req.Body)
		gotBody = string(b)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"code":0,"message":"OK","data":{"u-self":{"keyId":11,"keyData":"AAAA"},"u-peer":{"keyId":22,"keyData":"BBBB"}}}`,
			)),
		}, nil
	})
	keys, err := c.GetLastE2EEPublicKeysContext(context.Background(), "c-group-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/talk/thrift/Talk/TalkService/getLastE2EEPublicKeys" || gotBody != `["c-group-1"]` {
		t.Fatalf("unexpected RPC request: path=%q body=%q", gotPath, gotBody)
	}
	if len(keys) != 2 || keys["u-self"].KeyID != 11 || keys["u-peer"].KeyData != "BBBB" {
		t.Fatalf("unexpected keys: %+v", keys)
	}

	// Non-zero gateway code preserves TalkException classification without leaking body.
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":98,"reason":"member settings off","secret":"hide-me"}}`,
			)),
		}, nil
	})
	_, err = c.GetLastE2EEPublicKeysContext(context.Background(), "c-group-1")
	if !IsE2EEDisabled(err) || strings.Contains(err.Error(), "hide-me") {
		t.Fatalf("error misclassified or leaked: %v", err)
	}

	// HTTP 503 with a TalkException body is a gateway failure, never an explicit E2EE disable signal.
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":100,"reason":"exceed max member"}}`,
			)),
		}, nil
	})
	_, err = c.GetLastE2EEPublicKeysContext(context.Background(), "c-group-1")
	if err == nil || IsE2EEDisabled(err) {
		t.Fatalf("HTTP 503 must not classify as E2EE disabled: %v", err)
	}
}

func TestE2EEKeyMethodsRejectMissingCodeEnvelope(t *testing.T) {
	c := NewClient("token")
	// Malformed JSON missing "code" must NOT decode as code=0 and fall through to ErrE2EEDisabled.
	c.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":{"specVersion":-1,"allowedTypes":[]}}`)),
		}, nil
	})
	if _, err := c.NegotiateE2EEPublicKeyContext(context.Background(), "u-peer"); err == nil || IsE2EEDisabled(err) {
		t.Fatalf("NegotiateE2EEPublicKey missing code accepted or downgraded: %v", err)
	}
	if _, err := c.GetE2EEPublicKeyContext(context.Background(), "u-peer", 1, 22); err == nil || IsE2EEDisabled(err) {
		t.Fatalf("GetE2EEPublicKey missing code accepted or downgraded: %v", err)
	}
	if _, err := c.GetE2EEGroupSharedKeyContext(context.Background(), "c-group", 33); err == nil || IsGroupKeyNotFound(err) || IsE2EEDisabled(err) {
		t.Fatalf("GetE2EEGroupSharedKey missing code accepted: %v", err)
	}
	if _, err := c.GetLastE2EEGroupSharedKeyContext(context.Background(), "c-group"); err == nil || IsGroupKeyNotFound(err) || IsE2EEDisabled(err) {
		t.Fatalf("GetLastE2EEGroupSharedKey missing code accepted: %v", err)
	}
}
