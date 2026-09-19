package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Execute a synthetic child process through the production os/exec path. Direct
// Write calls alone would miss bytes.Buffer's promoted ReadFrom bypass.
func TestRunSecretToolOutputBounds(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestSecretToolOutputChild$'\n"
	if err := os.WriteFile(filepath.Join(dir, "secret-tool"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, mode := range []string{"stdout", "stderr", "missing", "success", "boundary"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LINE_CLI_TEST_SECRET_TOOL_OUTPUT", mode)
			r := runSecretTool([]string{"lookup"}, nil)
			defer clear(r.output)
			switch mode {
			case "stdout", "stderr":
				if !errors.Is(r.err, errHelperOutputLimit) || len(r.output) != 0 {
					t.Fatal("oversized helper output was not rejected and discarded")
				}
				store := secretToolStore{run: func([]string, []byte) secretResult { return r }}
				if _, err := store.loadKey(); err == nil || errors.Is(err, ErrNotFound) {
					t.Fatal("output limit failure mistaken for success or a missing key")
				}
				if err := store.Delete(); err == nil {
					t.Fatal("output limit failure mistaken for successful deletion")
				}
			case "missing":
				if r.err != nil || r.code != 1 || r.diagnostic || len(r.output) != 0 {
					t.Fatal("ordinary missing-key result changed")
				}
			case "success":
				if r.err != nil || r.code != 0 || string(r.output) != "synthetic" {
					t.Fatal("ordinary helper output changed")
				}
			case "boundary":
				if r.err != nil || r.code != 0 || len(r.output) != maxSessionBytes {
					t.Fatal("output exactly at the bound was rejected")
				}
			}
		})
	}
}

func TestSecretToolOutputChild(t *testing.T) {
	mode := os.Getenv("LINE_CLI_TEST_SECRET_TOOL_OUTPUT")
	if mode == "" {
		return
	}
	switch mode {
	case "stdout", "stderr", "boundary":
		destination, size := os.Stdout, maxSessionBytes+1
		if mode == "stderr" {
			destination = os.Stderr
		}
		if mode == "boundary" {
			size = maxSessionBytes
		}
		_, _ = destination.Write(bytes.Repeat([]byte("x"), size))
		if mode != "boundary" {
			os.Exit(1)
		}
	case "missing":
		os.Exit(1)
	case "success":
		_, _ = os.Stdout.WriteString("synthetic")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
