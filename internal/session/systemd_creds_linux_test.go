package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func syntheticSystemd(t *testing.T, mode string) systemdCredentials {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return systemdCredentials{path: path, version: 259, timeout: 5 * time.Second, prefix: []string{"-test.run=^TestSystemdChild$", "--", mode}}
}

// A synthetic pipe protocol, not systemd encryption. Real credentials are only
// exercised by the separately gated disposable-VM integration test.
func TestSystemdChild(t *testing.T) {
	i := slices.Index(os.Args, "--")
	if i < 0 {
		return
	}
	mode, args := os.Args[i+1], os.Args[i+2:]
	switch mode {
	case "timeout":
		time.Sleep(time.Minute)
	case "stdout":
		fmt.Print(strings.Repeat("x", 200000))
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 8192))
		os.Exit(1)
	case "failure":
		fmt.Fprint(os.Stderr, "SYNTHETIC_SECRET_MUST_NOT_ESCAPE")
		os.Exit(1)
	case "environment":
		for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "CREDENTIALS_DIRECTORY", "SYSTEMD_LOG_TARGET", "LINE_TEST_SECRET"} {
			if os.Getenv(name) != "" {
				os.Exit(1)
			}
		}
		fmt.Print("clean")
	case "valid", "wrong-length", "wrong-key", "null":
		if !slices.Contains(args, "--user") || !slices.Contains(args, "--name="+systemdCredentialName) || !slices.Contains(args, "--newline=no") {
			os.Exit(2)
		}
		input, err := io.ReadAll(io.LimitReader(os.Stdin, maxEncoded+1))
		if err != nil {
			os.Exit(2)
		}
		if slices.Contains(args, "encrypt") {
			if len(input) != 32 {
				os.Exit(2)
			}
			raw := fixture(hostUserID)
			copy(raw[80:112], input)
			if mode == "wrong-key" {
				raw[80] ^= 1
			}
			if mode == "null" {
				clear(raw[:16])
			}
			sum := sha256.Sum256(raw[:112])
			copy(raw[112:], sum[:16])
			_, _ = os.Stdout.Write(encode(raw))
		} else {
			raw, err := base64.StdEncoding.DecodeString(string(input))
			if err != nil || len(raw) != 128 {
				os.Exit(2)
			}
			sum := sha256.Sum256(raw[:112])
			if !bytes.Equal(sum[:16], raw[112:]) {
				os.Exit(1)
			}
			end := 112
			if mode == "wrong-length" {
				end--
			}
			_, _ = os.Stdout.Write(raw[80:end])
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestSystemdSubprocessBoundsCancellationAndEnvironment(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want error
	}{{"stdout", ErrCredentialOutput}, {"stderr", ErrCredentialOutput}, {"failure", ErrCredentialHelper}, {"timeout", ErrCredentialTimeout}} {
		t.Run(tc.mode, func(t *testing.T) {
			h := syntheticSystemd(t, tc.mode)
			if tc.mode == "timeout" {
				h.timeout = 100 * time.Millisecond
			}
			got, err := h.run(context.Background(), nil, nil, 32)
			if !errors.Is(err, tc.want) || len(got) != 0 || strings.Contains(fmt.Sprint(err), "SYNTHETIC_SECRET") {
				t.Fatal("unsafe subprocess result", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := syntheticSystemd(t, "valid").run(ctx, nil, nil, 32); !errors.Is(err, ErrCredentialCancelled) {
		t.Fatal(err)
	}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "CREDENTIALS_DIRECTORY", "SYSTEMD_LOG_TARGET", "LINE_TEST_SECRET"} {
		t.Setenv(name, "synthetic")
	}
	got, err := syntheticSystemd(t, "environment").run(context.Background(), nil, nil, 32)
	if err != nil || string(got) != "clean" {
		t.Fatal("environment was inherited", err)
	}
}

func TestSystemdSealingRequiresMatchingProtectionAndKey(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	for _, tc := range []struct {
		mode     string
		required protectionRequirement
		want     error
	}{
		{"valid", requireHost, nil}, {"valid", requireTPM, ErrCredentialPolicy}, {"wrong-length", requireHost, ErrStorageAuthentication},
		{"wrong-key", requireHost, ErrStorageAuthentication}, {"null", requireHost, ErrCredentialPolicy},
	} {
		t.Run(fmt.Sprint(tc.mode, tc.required), func(t *testing.T) {
			blob, p, err := syntheticSystemd(t, tc.mode).Seal(context.Background(), key, tc.required)
			if !errors.Is(err, tc.want) {
				t.Fatal("unexpected enrollment result", err)
			}
			if err != nil && (len(blob) != 0 || p != (credentialProtection{})) {
				t.Fatal("failed enrollment returned material")
			}
		})
	}
	// Validation precedes execution: this fake would otherwise time out.
	h := syntheticSystemd(t, "timeout")
	if _, err := h.Unseal(context.Background(), encode(fixture("00000000000000000000000000000000")), credentialProtection{}); !errors.Is(err, ErrCredentialPolicy) {
		t.Fatal(err)
	}
	p := credentialProtection{Scheme: "host-user", PCRMask: 1}
	if _, err := h.Unseal(context.Background(), encode(fixture(hostUserID)), p); !errors.Is(err, ErrCredentialPolicy) {
		t.Fatal(err)
	}
}

func TestSystemdVersionAndArgumentGates(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{{"systemd 255 (255.1)", false}, {"systemd 256 (256)", true}, {"systemd 257 (257.9)", true}, {"systemd 259 (259.5)", true}, {"systemd 260", false}, {"systemd 259evil", false}, {"not systemd", false}} {
		_, err := parseSystemdVersion([]byte(tc.value))
		if (err == nil) != tc.ok {
			t.Fatal("wrong version gate", tc.value)
		}
	}
	for _, v := range []int{256, 257, 258, 259} {
		h := systemdCredentials{version: v}
		args := h.args("decrypt")
		if slices.Contains(args, "--no-ask-password") != (v >= 259) || slices.Contains(args, "--refuse-null") != (v >= 259) {
			t.Fatal("incorrect version-specific flags")
		}
	}
}
