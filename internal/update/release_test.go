package update

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableVersionComparison(t *testing.T) {
	for _, tt := range []struct{ current, latest, want string }{
		{"v0.3.0", "v0.3.1", "update_available"}, {"v0.9.0", "v0.10.0", "update_available"},
		{"v1.0.0", "v0.10.0", "ahead"}, {"v0.3.1", "v0.3.1", "up_to_date"},
		{"v2.0.0", "v100000000000000000000000.0.0", "update_available"},
		{"dev", "v0.3.1", "unknown_version"}, {"v0.3.0-1-gabc", "v0.3.1", "unknown_version"},
		{"v0.4.0-rc.1", "v0.3.1", "unknown_version"}, {"v00.3.0", "v0.3.1", "unknown_version"},
		{"v0.3.0", "v0.3.1\n", "unknown_version"},
	} {
		if got := versionStatus(tt.current, tt.latest); got != tt.want {
			t.Errorf("%s vs %s: %s", tt.current, tt.latest, got)
		}
	}
}

func TestCheckUsesOnlyOfficialMetadata(t *testing.T) {
	s := testService(t)
	calls := 0
	s.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != latestAPI || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected request")
		}
		return response(200, metadata("v0.3.1")), nil
	})}
	p, err := s.Check(context.Background())
	if err != nil || !p.CanSelfUpdate || p.Status != "update_available" || p.Installation != "standalone" || calls != 1 {
		t.Fatalf("%+v, %v", p, err)
	}
	path, _ := s.executable()
	got, _ := os.ReadFile(path)
	if string(got) != "original executable" {
		t.Fatal("check changed executable")
	}
}

func TestReleaseErrorsAreBoundedAndDoNotLeakBodies(t *testing.T) {
	for _, tt := range []struct {
		code int
		body string
	}{
		{403, "secret body"}, {429, "secret body"}, {404, "secret body"}, {502, "secret body"},
		{200, "secret body"}, {200, strings.Repeat("x", maxMetadata+1)},
		{200, `{"tag_name":"v0.3.1","draft":true}`}, {200, `{"tag_name":"v0.3.1","prerelease":true}`},
		{200, `{"tag_name":"../../bad"}`}, {200, `{"tag_name":"v0.3.1"} {}`},
	} {
		s := testService(t)
		s.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(tt.code, tt.body), nil })}
		_, err := s.Check(context.Background())
		if err == nil || strings.Contains(err.Error(), "secret body") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestMissingOrForeignAssetsRequireManualUpdate(t *testing.T) {
	for _, body := range []string{`{"tag_name":"v0.3.1"}`, strings.ReplaceAll(metadata("v0.3.1"), "github.com", "example.com")} {
		s := testService(t)
		s.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}
		p, err := s.Check(context.Background())
		if err != nil || p.CanSelfUpdate || p.Status != "update_available" {
			t.Fatalf("%+v %v", p, err)
		}
	}
}

func TestInstallationOwnership(t *testing.T) {
	s := testService(t)
	path, _ := s.executable()
	if s.installation(path) != "standalone" {
		t.Fatal("installer receipt ignored")
	}
	s.distribution = "source"
	if s.installation(path) != "source" {
		t.Fatal("receipt overrode source build")
	}
	brew := filepath.Join(t.TempDir(), "Cellar", "line-cli", "0.3.0", "bin", "line")
	if s.installation(brew) != "homebrew" {
		t.Fatal("Homebrew not detected")
	}
	s.distribution = "standalone"
	if s.installation(brew) != "homebrew" {
		t.Fatal("release marker overrode package ownership")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), receiptName)); err != nil {
		t.Fatal(err)
	}
	if s.installation(path) != "unknown" {
		t.Fatal("unmanaged binary accepted")
	}
}

func TestChecksumSelection(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, body := range []string{hash + "  other.tar.gz", "bad  line.tar.gz", hash + "  line.tar.gz\n" + hash + "  line.tar.gz"} {
		if _, err := checksumFor([]byte(body), "line.tar.gz"); err == nil {
			t.Fatal("accepted missing or invalid checksum")
		}
	}
	got, err := checksumFor([]byte(strings.ToUpper(hash)+" *line.tar.gz\r\n"), "line.tar.gz")
	if err != nil || got != hash {
		t.Fatal(got, err)
	}
}

func TestWindowsGuidancePreservesCustomDirectory(t *testing.T) {
	s := testService(t)
	oldPath, _ := s.executable()
	dir := filepath.Join(filepath.Dir(oldPath), "someone's custom directory")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "line.exe")
	if err := os.WriteFile(path, []byte("executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, receiptName), []byte(receiptContent), 0644); err != nil {
		t.Fatal(err)
	}
	s.executable = func() (string, error) { return path, nil }
	s.goos = "windows"
	s.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(200, metadata("v0.3.1")), nil })}
	p, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.CanSelfUpdate || !strings.Contains(p.UpgradeCommand, "someone''s custom directory") || !strings.Contains(p.UpgradeCommand, "LINE_CLI_INSTALL_DIR") {
		t.Fatalf("incorrect Windows guidance: %+v", p)
	}
}

func TestNoUpgradeInstructionsWhenCurrentOrAhead(t *testing.T) {
	for _, version := range []string{"v0.3.1", "v0.4.0"} {
		s := testService(t)
		s.current = version
		s.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(200, metadata("v0.3.1")), nil })}
		p, err := s.Check(context.Background())
		if err != nil || p.Instructions != "" || p.UpgradeCommand != "" {
			t.Fatalf("unexpected upgrade instructions: %+v %v", p, err)
		}
	}
}
