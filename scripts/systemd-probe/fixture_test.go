package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureRoundtripRebootAndExclusiveCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.cred")
	h := fakeHelper(t, "valid")
	if got := enrollFixture(h, 259, path, "first-boot"); got != "enrolled" {
		t.Fatal(got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("fixture permissions")
	}
	before, _ := os.ReadFile(path)
	if got := enrollFixture(h, 259, path, "first-boot"); got != "fixture_create_failed" {
		t.Fatal("overwrote fixture")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("changed fixture")
	}
	for _, tc := range []struct {
		boot     string
		required bool
		want     string
		reboot   bool
	}{
		{"first-boot", false, "fixture_verified", false},
		{"first-boot", true, "reboot_not_observed", false},
		{"second-boot", true, "fixture_verified", true},
	} {
		got, reboot := verifyFixture(h, 259, path, tc.boot, tc.required, false)
		if got != tc.want || reboot != tc.reboot {
			t.Fatalf("unexpected verification: %s, %v", got, reboot)
		}
	}
	if got, _ := verifyFixture(h, 259, path, "first-boot", false, true); got != "expected_rejection_missing" {
		t.Fatal(got)
	}
	if got, _ := verifyFixture(fakeHelper(t, "failure"), 259, path, "first-boot", false, true); got != "expected_helper_rejection" {
		t.Fatal(got)
	}
	if got, _ := verifyFixture(fakeHelper(t, "failure"), 259, path+"-absent", "first-boot", false, true); got != "fixture_read_failed" {
		t.Fatal("filesystem denial mistaken for helper rejection")
	}
	var fixture rebootFixture
	if json.Unmarshal(before, &fixture) != nil {
		t.Fatal("fixture decode")
	}
	fixture.Digest[0] ^= 1
	data, _ := json.Marshal(fixture)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, _ := verifyFixture(h, 259, path, "first-boot", false, false); got != "fixture_key_mismatch" {
		t.Fatal(got)
	}
}
