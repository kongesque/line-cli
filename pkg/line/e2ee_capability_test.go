package line

import (
	"errors"
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
