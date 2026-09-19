package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// This explicitly synthetic fixture is not a LINE session or production format.
// The digest checks a random key across boots without persisting that key.
type rebootFixture struct {
	Kind   string   `json:"kind"`
	Boot   string   `json:"boot"`
	Blob   []byte   `json:"blob"`
	Digest [32]byte `json:"digest"`
}

func linuxBootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	s := strings.TrimSpace(string(b))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(s) {
		return "", errors.New("boot ID unavailable")
	}
	return s, nil
}

func enrollFixture(h helper, version int, path, boot string) string {
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return "random_source_failed"
	}
	blob, err := h.run(credentialArgs(version, "encrypt", credentialName), key, maxEncoded)
	if err != nil {
		return classify(err)
	}
	defer clear(blob)
	if _, err := inspectCredential(blob); err != nil {
		return "envelope_rejected"
	}
	plain, err := h.run(credentialArgs(version, "decrypt", credentialName), blob, 32)
	defer clear(plain)
	if err != nil || len(plain) != 32 || subtle.ConstantTimeCompare(plain, key) != 1 {
		return "roundtrip_failed"
	}
	data, err := json.Marshal(rebootFixture{Kind: "line-cli-phase0-synthetic-v1", Boot: boot, Blob: blob, Digest: sha256.Sum256(key)})
	if err != nil {
		return "fixture_encode_failed"
	}
	defer clear(data)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "fixture_create_failed"
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "fixture_write_failed"
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "fixture_write_failed"
	}
	syncErr = dir.Sync()
	dir.Close()
	if syncErr != nil {
		return "fixture_write_failed"
	}
	return "enrolled"
}

func verifyFixture(h helper, version int, path, boot string, requireReboot, expectDenied bool) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "fixture_read_failed", false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "fixture_read_failed", false
	}
	data, err := io.ReadAll(io.LimitReader(f, 2*maxEncoded+1))
	defer clear(data)
	if err != nil || len(data) > 2*maxEncoded {
		return "fixture_read_failed", false
	}
	var fixture rebootFixture
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil || fixture.Kind != "line-cli-phase0-synthetic-v1" || fixture.Boot == "" {
		return "fixture_rejected", false
	}
	defer clear(fixture.Blob)
	if decoder.Decode(new(any)) != io.EOF {
		return "fixture_rejected", false
	}
	if _, err := inspectCredential(fixture.Blob); err != nil {
		return "envelope_rejected", false
	}
	plain, err := h.run(credentialArgs(version, "decrypt", credentialName), fixture.Blob, 32)
	defer clear(plain)
	if expectDenied {
		if errors.Is(err, errHelper) {
			return "expected_helper_rejection", false
		}
		return "expected_rejection_missing", false
	}
	if err != nil {
		return classify(err), false
	}
	digest := sha256.Sum256(plain)
	if len(plain) != 32 || subtle.ConstantTimeCompare(digest[:], fixture.Digest[:]) != 1 {
		return "fixture_key_mismatch", false
	}
	changed := boot != fixture.Boot
	if requireReboot && !changed {
		return "reboot_not_observed", false
	}
	return "fixture_verified", changed
}
