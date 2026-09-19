package session

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

const systemdCredentialName = "line-cli-session-key"

type protectionRequirement uint8

const (
	requireHost protectionRequirement = iota + 1 // caller must first obtain explicit host-only acceptance
	requireTPM
)

type sealedKeyProvider interface {
	Seal(context.Context, []byte, protectionRequirement) ([]byte, credentialProtection, error)
	Unseal(context.Context, []byte, credentialProtection) ([]byte, error)
}

type systemdCredentials struct {
	path    string
	version int
	timeout time.Duration
	// Only synthetic subprocess tests use a prefix; no user configuration does.
	prefix []string
}

func newSystemdCredentials(ctx context.Context) (sealedKeyProvider, error) {
	if os.Geteuid() == 0 {
		return nil, ErrHeadlessUnavailable
	}
	path, err := trustedSystemdPath()
	if err != nil {
		return nil, err
	}
	h := systemdCredentials{path: path, timeout: 10 * time.Second}
	version, err := h.run(ctx, []string{"--version"}, nil, 8192)
	defer clear(version)
	if err != nil {
		return nil, err
	}
	h.version, err = parseSystemdVersion(version)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func parseSystemdVersion(data []byte) (int, error) {
	match := regexp.MustCompile(`^systemd ([0-9]+)\b`).FindSubmatch(data)
	if len(match) != 2 {
		return 0, ErrHeadlessUnavailable
	}
	version, err := strconv.Atoi(string(match[1]))
	// 256 is the source-inspected feature floor; 257 and 259 have native VM
	// evidence. Future helper generations require review before enabling them.
	if err != nil || version < 256 || version > 259 {
		return 0, ErrHeadlessUnavailable
	}
	return version, nil
}

func trustedSystemdPath() (string, error) {
	path, err := filepath.EvalSymlinks("/usr/bin/systemd-creds")
	if err != nil {
		return "", ErrHeadlessUnavailable
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return "", ErrHeadlessUnavailable
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return "", ErrHeadlessUnavailable
		}
		if current == path {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				return "", ErrHeadlessUnavailable
			}
		} else if !info.IsDir() {
			return "", ErrHeadlessUnavailable
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return path, nil
}

type credentialOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *credentialOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		b.exceeded = true
		return 0, ErrCredentialOutput
	}
	return b.buffer.Write(data)
}

func (h systemdCredentials) run(parent context.Context, args []string, input []byte, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, h.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.path, append(append([]string{}, h.prefix...), args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_LOG_LEVEL=err", "SYSTEMD_PAGER=cat"}
	cmd.Stdin = bytes.NewReader(input)
	stdout, stderr := &credentialOutput{limit: limit}, &credentialOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	defer clear(stderr.buffer.Bytes())
	if err != nil || stdout.exceeded || stderr.exceeded {
		clear(stdout.buffer.Bytes())
		switch {
		case stdout.exceeded || stderr.exceeded:
			return nil, ErrCredentialOutput
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, ErrCredentialCancelled
		case ctx.Err() != nil:
			return nil, ErrCredentialTimeout
		default:
			return nil, ErrCredentialHelper
		}
	}
	return stdout.buffer.Bytes(), nil
}

func (h systemdCredentials) args(verb string) []string {
	args := []string{"--user", "--name=" + systemdCredentialName, "--newline=no"}
	if h.version >= 259 {
		args = append(args, "--no-ask-password")
		if verb == "decrypt" {
			args = append(args, "--refuse-null")
		}
	}
	return append(args, verb, "-", "-")
}

func (h systemdCredentials) Seal(ctx context.Context, key []byte, required protectionRequirement) ([]byte, credentialProtection, error) {
	var zero credentialProtection
	if len(key) != 32 {
		return nil, zero, ErrCredentialPolicy
	}
	mode := "host"
	if required == requireTPM {
		mode = "host+tpm2"
	} else if required != requireHost {
		return nil, zero, ErrCredentialPolicy
	}
	blob, err := h.run(ctx, append([]string{"--with-key=" + mode}, h.args("encrypt")...), key, maxEncoded)
	if err != nil {
		return nil, zero, err
	}
	p, err := inspectCredential(blob)
	// Signed-PCR enrollment is not enabled by the current evidence gate.
	if err != nil || (required == requireHost && p.Scheme != "host-user") ||
		(required == requireTPM && p.Scheme != "host-tpm2-user") {
		clear(blob)
		return nil, zero, ErrCredentialPolicy
	}
	got, err := h.Unseal(ctx, blob, p)
	defer clear(got)
	if err != nil {
		clear(blob)
		return nil, zero, err
	}
	if subtle.ConstantTimeCompare(got, key) != 1 {
		clear(blob)
		return nil, zero, ErrStorageAuthentication
	}
	return blob, p, nil
}

func (h systemdCredentials) Unseal(ctx context.Context, blob []byte, expected credentialProtection) ([]byte, error) {
	p, err := inspectCredential(blob)
	if err != nil || p != expected || p.Scheme == "host-tpm2-signed-user" {
		return nil, ErrCredentialPolicy
	}
	key, err := h.run(ctx, h.args("decrypt"), blob, 32)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		clear(key)
		return nil, ErrStorageAuthentication
	}
	return key, nil
}
