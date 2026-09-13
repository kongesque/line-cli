package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"
)

func envelopeState() *State {
	revision := int64(172)
	return &State{Version: 1, MID: "synthetic-mid", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh",
		Email: "synthetic@example.test", Certificate: "synthetic-cert", Generation: "synthetic-generation",
		RefreshAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), LastReqSeq: 73, WatchRevision: &revision,
		ExportedKeys: map[string]string{"key": "synthetic-letter-sealing-material"}}
}

func testEnvelope(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, blob := bytes.Repeat([]byte{17}, 32), encode(fixture(hostUserID))
	p, err := inspectCredential(blob)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeEnvelope(envelopeState(), blob, key, p)
	if err != nil {
		t.Fatal(err)
	}
	return data, key
}

func TestEnvelopeRoundtripAndAuthenticatedHeader(t *testing.T) {
	data, key := testEnvelope(t)
	e, err := parseEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.open(key)
	if err != nil || !reflect.DeepEqual(state, envelopeState()) {
		t.Fatal("lost encrypted state", err)
	}
	for _, secret := range []string{state.MID, state.Email, state.AccessToken, state.RefreshToken, state.ExportedKeys["key"]} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("unencrypted state in envelope")
		}
	}
	// Every bit position is either structurally rejected or fails AEAD. This
	// covers sealed-credential bytes, policy, nonce, header sizes and ciphertext.
	for i := range data {
		changed := bytes.Clone(data)
		changed[i] ^= 1
		parsed, err := parseEnvelope(changed)
		if err == nil {
			_, err = parsed.open(key)
		}
		if err == nil {
			t.Fatalf("unauthenticated byte at %d", i)
		}
	}
	if _, err := e.open(bytes.Repeat([]byte{18}, 32)); !errors.Is(err, ErrStorageAuthentication) {
		t.Fatal(err)
	}
	updated, err := encodeEnvelope(state, e.sealed, key, e.protection)
	if err != nil {
		t.Fatal(err)
	}
	other, err := parseEnvelope(updated)
	if err != nil || bytes.Equal(e.nonce, other.nonce) || !bytes.Equal(e.sealed, other.sealed) {
		t.Fatal("nonce or sealed-key reuse error", err)
	}
}

func TestEnvelopeBoundsVersionsAndTruncation(t *testing.T) {
	data, key := testEnvelope(t)
	for n := range data {
		e, err := parseEnvelope(data[:n])
		if err == nil {
			_, err = e.open(key)
		}
		if err == nil {
			t.Fatalf("truncation accepted at %d", n)
		}
	}
	for _, offset := range []int{16, 18, 20, 24, 25, 28, 32, 34, 36} {
		changed := bytes.Clone(data)
		changed[offset] = 255
		if _, err := parseEnvelope(changed); err == nil {
			t.Fatalf("invalid header accepted at %d", offset)
		}
	}
	changed := bytes.Clone(data)
	binary.LittleEndian.PutUint32(changed[20:24], ^uint32(0))
	if _, err := parseEnvelope(changed); err == nil {
		t.Fatal("integer overflow accepted")
	}
	if _, err := parseEnvelope(bytes.Repeat([]byte{0}, maxSessionBytes+1)); err == nil {
		t.Fatal("oversized file accepted")
	}
	e, _ := parseEnvelope(data)
	state := envelopeState()
	state.ExportedKeys["large"] = string(bytes.Repeat([]byte{'x'}, maxSessionBytes))
	if _, err := encodeEnvelope(state, e.sealed, key, e.protection); err == nil {
		t.Fatal("oversized state accepted")
	}
}

func FuzzEnvelope(f *testing.F) {
	blob := encode(fixture(hostUserID))
	p, _ := inspectCredential(blob)
	data, _ := encodeEnvelope(envelopeState(), blob, make([]byte, 32), p)
	f.Add(data)
	f.Add([]byte(envelopeMagic))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := parseEnvelope(data)
		if err == nil {
			_, _ = e.open(make([]byte, 32))
		}
	})
}
