package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// Fixtures describe header layouts only; they are not authentic systemd blobs.
func fixture(id string) []byte {
	b := make([]byte, 48)
	idBytes, _ := hex.DecodeString(id)
	copy(b, idBytes)
	for i, v := range []uint32{32, 1, 12, 16} {
		binary.LittleEndian.PutUint32(b[16+i*4:], v)
	}
	if id == tpmUserID || id == pkUserID {
		h := make([]byte, 56)
		binary.LittleEndian.PutUint64(h, 1<<7)
		binary.LittleEndian.PutUint16(h[8:], 11)
		binary.LittleEndian.PutUint16(h[10:], 0x23)
		binary.LittleEndian.PutUint32(h[12:], 4)
		binary.LittleEndian.PutUint32(h[16:], 32)
		b = append(b, h...)
		if id == pkUserID {
			h := make([]byte, 16)
			binary.LittleEndian.PutUint64(h, 1<<11)
			binary.LittleEndian.PutUint32(h[8:], 4)
			b = append(b, h...)
		}
	}
	h := make([]byte, 8+24+32+16)
	binary.LittleEndian.PutUint64(h, 7)
	return append(b, h...)
}

func encode(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func TestCredentialLayouts(t *testing.T) {
	for _, tc := range []struct {
		id, scheme  string
		pcr, signed uint64
	}{
		{hostUserID, "host-user", 0, 0},
		{tpmUserID, "host-tpm2-user", 1 << 7, 0},
		{pkUserID, "host-tpm2-signed-user", 1 << 7, 1 << 11},
	} {
		t.Run(tc.scheme, func(t *testing.T) {
			got, err := inspectCredential(append(encode(fixture(tc.id)), '\n'))
			if err != nil || got.Scheme != tc.scheme || got.PCRMask != tc.pcr || got.SignedPCR != tc.signed {
				t.Fatal("layout classification failed")
			}
		})
	}
}

func TestCredentialRejectsEveryTruncation(t *testing.T) {
	for _, id := range []string{hostUserID, tpmUserID, pkUserID} {
		b := fixture(id)
		for n := range b {
			if _, err := inspectCredential(encode(b[:n])); err == nil {
				t.Fatalf("accepted truncated fixture at %d", n)
			}
		}
	}
}

func TestCredentialRejectsUnsupportedSchemesAndFields(t *testing.T) {
	for _, id := range []string{
		"058469daf6f54324800549da0f8ea2fb", // null
		"5a1c6a86df9d4096b1d5a65e0862f19a", // system host
		"0c7cc07b117645919c4b0bea08bc20fe", // TPM-only
		"00000000000000000000000000000000",
	} {
		if _, err := inspectCredential(encode(fixture(id))); err == nil {
			t.Fatal("accepted forbidden identifier")
		}
	}
	for _, tc := range []struct {
		name   string
		id     string
		mutate func([]byte)
	}{
		{"key size", hostUserID, func(b []byte) { b[16] = 16 }},
		{"block size", hostUserID, func(b []byte) { b[20] = 16 }},
		{"iv size", hostUserID, func(b []byte) { b[24] = 0 }},
		{"tag size", hostUserID, func(b []byte) { b[28] = 0 }},
		{"padding", hostUserID, func(b []byte) { b[44] = 1 }},
		{"scope flags", hostUserID, func(b []byte) { b[48] = 0 }},
		{"PCR mask", tpmUserID, func(b []byte) { b[51] = 1 }},
		{"PCR bank", tpmUserID, func(b []byte) { b[56] = 0 }},
		{"primary algorithm", tpmUserID, func(b []byte) { b[58] = 0 }},
		{"blob bounds", tpmUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[60:], ^uint32(0)) }},
		{"policy bounds", tpmUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[64:], ^uint32(0)) }},
		{"signed PCR mask", pkUserID, func(b []byte) { clear(b[104:112]) }},
		{"public key bounds", pkUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[112:], ^uint32(0)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixture(tc.id)
			tc.mutate(b)
			if _, err := inspectCredential(encode(b)); err == nil {
				t.Fatal("accepted invalid field")
			}
		})
	}
	for _, b := range [][]byte{nil, []byte("invalid-base64!"), bytes.Repeat([]byte{'A'}, maxEncoded+1), encode(make([]byte, maxBlob+1))} {
		if _, err := inspectCredential(b); err == nil {
			t.Fatal("accepted invalid encoding/size")
		}
	}
}

func FuzzCredential(f *testing.F) {
	for _, id := range []string{hostUserID, tpmUserID, pkUserID} {
		f.Add(encode(fixture(id)))
	}
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := inspectCredential(b)
		if err == nil && !slices.Contains([]string{"host-user", "host-tpm2-user", "host-tpm2-signed-user"}, p.Scheme) {
			t.Fatal("unexpected scheme")
		}
	})
}

func fakeHelper(t *testing.T, mode string) helper {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return helper{path: path, timeout: 5 * time.Second, prefix: []string{"-test.run=^TestHelperProcess$", "--", mode}}
}

// Fake transport, not a systemd emulator. The checksum only detects modifications
// in test messages. It deliberately does not claim credential authentication.
func TestHelperProcess(t *testing.T) {
	i := slices.Index(os.Args, "--")
	if i < 0 {
		return
	}
	mode, args := os.Args[i+1], os.Args[i+2:]
	switch mode {
	case "timeout":
		time.Sleep(time.Minute)
	case "stdout-limit":
		fmt.Print(strings.Repeat("x", 16384))
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 16384))
	case "failure":
		fmt.Fprint(os.Stderr, "SYNTHETIC_SECRET_MUST_NOT_ESCAPE")
		os.Exit(1)
	case "environment":
		if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" || os.Getenv("CREDENTIALS_DIRECTORY") != "" || os.Getenv("PHASE0_TEST_SECRET") != "" {
			os.Exit(1)
		}
		fmt.Print("clean")
	case "valid", "wrong-length", "accept-tamper":
		b, err := io.ReadAll(io.LimitReader(os.Stdin, maxEncoded+1))
		if err != nil {
			os.Exit(1)
		}
		if slices.Contains(args, "encrypt") {
			if len(b) != 32 {
				os.Exit(1)
			}
			raw := fixture(hostUserID)
			copy(raw[80:112], b)
			sum := sha256.Sum256(raw[:112])
			copy(raw[112:], sum[:16])
			os.Stdout.Write(encode(raw))
		} else {
			if !slices.Contains(args, "--name="+credentialName) {
				os.Exit(1)
			}
			raw, err := base64.StdEncoding.DecodeString(string(b))
			if err != nil || len(raw) != 128 {
				os.Exit(1)
			}
			sum := sha256.Sum256(raw[:112])
			if mode != "accept-tamper" && !bytes.Equal(sum[:16], raw[112:]) {
				os.Exit(1)
			}
			if mode == "wrong-length" {
				os.Stdout.Write(raw[80:111])
			} else {
				os.Stdout.Write(raw[80:112])
			}
		}
	default:
		os.Exit(1)
	}
	os.Exit(0)
}

func TestHelperLimitsAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want error
	}{
		{"failure", errHelper}, {"stdout-limit", errOutput}, {"stderr-limit", errOutput}, {"timeout", errTimeout},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			h := fakeHelper(t, tc.mode)
			if tc.mode == "timeout" {
				h.timeout = 100 * time.Millisecond
			}
			out, err := h.run(nil, nil, 4096)
			if !errors.Is(err, tc.want) || len(out) != 0 || strings.Contains(fmt.Sprint(err), "SYNTHETIC_SECRET") {
				t.Fatal("unsafe helper result")
			}
		})
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "synthetic")
	t.Setenv("CREDENTIALS_DIRECTORY", "/synthetic")
	t.Setenv("PHASE0_TEST_SECRET", "synthetic")
	out, err := fakeHelper(t, "environment").run(nil, nil, 32)
	if err != nil || string(out) != "clean" {
		t.Fatal("inherited environment")
	}
}

func TestObserveRequiresRoundtripAndNegativeChecks(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	for _, tc := range []struct{ mode, want string }{
		{"valid", "verified"}, {"wrong-length", "roundtrip_failed"}, {"accept-tamper", "negative_check_failed"},
	} {
		o := observe(fakeHelper(t, tc.mode), 259, "host+tpm2", key)
		if o.Outcome != tc.want {
			t.Fatalf("%s: %s", tc.mode, o.Outcome)
		}
		if o.Matches {
			t.Fatal("silently accepted host as requested TPM mode")
		}
		if tc.mode == "valid" && (o.Verified == nil || o.Verified.Scheme != "host-user" || !o.Negative) {
			t.Fatal("missing verified result")
		}
		if tc.mode == "wrong-length" && o.Verified != nil {
			t.Fatal("reported unverified protection")
		}
		b, _ := json.Marshal(o)
		if bytes.Contains(b, key) || bytes.Contains(b, encode(key)) {
			t.Fatal("report contains key")
		}
	}
}
