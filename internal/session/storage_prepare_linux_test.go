package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxPreflightPreservesSessionAndDefaultKey(t *testing.T) {
	for _, existing := range []bool{false, true} {
		s, service := syntheticFileStore(t)
		var original, key []byte
		if existing {
			if err := s.Save(&State{Version: 1, MID: "synthetic", AccessToken: "synthetic"}); err != nil {
				t.Fatal(err)
			}
			original, _ = os.ReadFile(s.path)
			key = append([]byte(nil), service.values["default"]...)
		}
		if err := prepareSecretFileStore(s); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(s.path)
		if existing && (err != nil || !bytes.Equal(got, original) || !bytes.Equal(key, service.values["default"])) {
			t.Fatal("preflight changed active storage", err)
		}
		if !existing && !errors.Is(err, os.ErrNotExist) {
			t.Fatal("preflight created active session", err)
		}
		entries, err := os.ReadDir(filepath.Dir(s.path))
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if existing {
			want = 1
		}
		if len(entries) != want || len(service.values) != want {
			t.Fatal("temporary state was not cleaned up")
		}
	}
}

func TestLinuxPreflightStopsOnLostKeyAndCleanupFailure(t *testing.T) {
	s, service := syntheticFileStore(t)
	if err := s.Save(&State{Version: 1, MID: "synthetic", AccessToken: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(s.path)
	key := service.values["default"]
	delete(service.values, "default")
	if err := prepareSecretFileStore(s); !errors.Is(err, errMissingWrappingKey) || errors.Is(err, ErrNotFound) {
		t.Fatal("accepted existing session with lost key")
	}
	if len(service.values) != 0 {
		t.Fatal("regenerated a lost wrapping key")
	}
	service.values["default"] = key
	service.fault = func(action, account string) bool {
		return action == "clear" && strings.HasPrefix(account, ".storage-probe-")
	}
	if err := prepareSecretFileStore(s); !errors.Is(err, ErrStorageCleanup) {
		t.Fatal("cleanup failure accepted", err)
	}
	got, _ := os.ReadFile(s.path)
	if !bytes.Equal(got, original) || !bytes.Equal(service.values["default"], key) {
		t.Fatal("cleanup touched active storage")
	}
}

func TestLinuxPreflightCleansUpFailedProbeSave(t *testing.T) {
	s, service := syntheticFileStore(t)
	service.fault = func(action, account string) bool {
		return action == "store" && strings.HasPrefix(account, ".storage-probe-")
	}
	if err := prepareSecretFileStore(s); err == nil {
		t.Fatal("failed save passed preflight")
	}
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil || len(entries) != 0 || len(service.values) != 0 {
		t.Fatal("failed probe leaked state", err)
	}
}
