package update

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shared synthetic fixtures for package tests.
type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "line")
	if err := os.WriteFile(exe, []byte("original executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, receiptName), []byte(receiptContent), 0644); err != nil {
		t.Fatal(err)
	}
	s := New("v0.3.0", "standalone")
	s.goos, s.arch = "linux", "amd64"
	s.executable = func() (string, error) { return exe, nil }
	return s
}

func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func metadata(tag string) string {
	name := archiveName("linux", "amd64")
	return fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q},{"name":"SHA256SUMS.txt","browser_download_url":%q}]}`,
		tag, name, assetURL(tag, name), assetURL(tag, "SHA256SUMS.txt"))
}
