package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

const downloadChat = "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type mediaDownloadAPI struct {
	session.API
	message    *line.Message
	data       []byte
	err        error
	downloads  int
	onDownload func()
}

func (f *mediaDownloadAPI) GetRecentMessagesV2(string, int) ([]*line.Message, error) {
	return []*line.Message{f.message}, nil
}
func (f *mediaDownloadAPI) DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error) {
	f.downloads++
	if f.onDownload != nil {
		f.onDownload()
	}
	return f.data, f.err
}
func downloadApp(t *testing.T, kind int) (*App, *mediaDownloadAPI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	a, out, diag := testApp(nil)
	api := &mediaDownloadAPI{message: &line.Message{ID: "123", ContentType: kind}, data: []byte{0, 1, 255, 254, 10, 13, 0}}
	a.Manager.NewClient = func(string) session.API { return api }
	return a, api, out, diag
}
func downloadArgs(output string) []string {
	return []string{"download", downloadChat, "--message", "123", "--output", output}
}

func TestDownloadBinaryStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, kind := range []int{1, 2, 3, 14} {
		a, api, out, diag := downloadApp(t, kind)
		if err := a.Run(downloadArgs("-")); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), api.data) || diag.Len() != 0 || api.downloads != 1 {
			t.Fatal("binary stdout was changed, annotated, or retried")
		}
		files, err := os.ReadDir(".")
		if err != nil || len(files) != 0 {
			t.Fatal("stdout created a local file", err)
		}
	}
	a, api, out, _ := downloadApp(t, 1)
	api.data = nil
	if err := a.Run(downloadArgs("-")); err != nil || out.Len() != 0 {
		t.Fatal("empty attachment failed", err)
	}
}

func TestDownloadStdoutValidationBeforeSession(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		args                  []string
		terminal, interactive bool
		want                  string
	}{
		{"json", append(downloadArgs("-"), "--json"), false, false, "cannot be combined"},
		{"terminal", downloadArgs("-"), true, true, "terminal"},
		{"redirected stdin", downloadArgs("-"), true, false, "terminal"},
		{"missing chat", []string{"download", "--message", "123", "--output", "-"}, false, true, "chat"},
		{"missing message", []string{"download", downloadChat, "--output", "-"}, false, true, "message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, out, _ := downloadApp(t, 1)
			a.Interactive, a.StdoutIsTerminal = tc.interactive, tc.terminal
			a.Lock = func() (func(), error) { t.Fatal("invalid stdout options opened a session"); return nil, nil }
			err := a.Run(tc.args)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) || out.Len() != 0 {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

type failedDownloadWriter struct {
	calls int
	err   error
}

func (w *failedDownloadWriter) Write(p []byte) (int, error) { w.calls++; return len(p) / 2, w.err }
func TestDownloadStdoutWriteFailureDoesNotRetry(t *testing.T) {
	for _, writeErr := range []error{nil, io.ErrClosedPipe} {
		a, api, _, _ := downloadApp(t, 2)
		writer := &failedDownloadWriter{err: writeErr}
		a.Out = writer
		want := writeErr
		if want == nil {
			want = io.ErrShortWrite
		}
		if err := a.Run(downloadArgs("-")); !errors.Is(err, want) || api.downloads != 1 || writer.calls != 1 {
			t.Fatalf("writer error/retry: %v", err)
		}
	}
}

func TestDownloadFailurePublishesNothing(t *testing.T) {
	for _, outputMode := range []string{"stdout", "file"} {
		for _, failure := range []string{"transport", "encrypted", "unsupported", "cancelled", "cancelled after fetch"} {
			t.Run(outputMode+"/"+failure, func(t *testing.T) {
				dir := t.TempDir()
				output := filepath.Join(dir, "saved.bin")
				if outputMode == "stdout" {
					output = "-"
				}
				a, api, out, _ := downloadApp(t, 1)
				switch failure {
				case "transport":
					api.err = errors.New("private response body")
				case "encrypted":
					api.message.Chunks = []string{"private ciphertext"}
					api.message.ContentMetadata = map[string]string{"OID": "encrypted-object"}
				case "unsupported":
					api.message.ContentType = 7
				case "cancelled", "cancelled after fetch":
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					a.Context = ctx
					if failure == "cancelled" {
						cancel()
					} else {
						api.onDownload = cancel
					}
				}
				err := a.Run(downloadArgs(output))
				if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private") {
					t.Fatalf("unsafe failure: %v", err)
				}
				files, readErr := os.ReadDir(dir)
				if readErr != nil || len(files) != 0 {
					t.Fatal("failed download left output/temp files", readErr)
				}
			})
		}
	}
}

func TestDownloadPathsKeepAtomicNoOverwriteBehavior(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, kind := range []int{1, 2, 3, 14} {
		a, api, out, _ := downloadApp(t, kind)
		path := fmt.Sprintf("media-%d.bin", kind)
		if err := a.Run(append(downloadArgs(path), "--json")); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, api.data) {
			t.Fatal("incorrect media bytes", err)
		}
		var summary map[string]any
		if err := json.Unmarshal(out.Bytes(), &summary); err != nil || len(summary) != 3 || summary["path"] != path || summary["message_id"] != "123" || summary["bytes"] != float64(len(data)) {
			t.Fatal("JSON contract changed", err)
		}
	}
	a, api, _, _ := downloadApp(t, 1)
	if err := a.Run(downloadArgs("./-")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("-")
	if err != nil || !bytes.Equal(data, api.data) {
		t.Fatal("literal dash file failed", err)
	}
	if err := os.Mkdir("existing-dir", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", "dangling"); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	for _, path := range []string{"./-", "existing-dir", "dangling"} {
		a, _, _, _ := downloadApp(t, 1)
		a.Lock = func() (func(), error) { t.Fatal("existing path opened a session"); return nil, nil }
		if err := a.Run(downloadArgs(path)); err == nil {
			t.Fatal("existing destination accepted")
		}
	}
}

func TestDownloadDestinationCreatedDuringFetchIsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.bin")
	a, api, out, _ := downloadApp(t, 1)
	api.onDownload = func() {
		if err := os.WriteFile(path, []byte("other process"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Run(downloadArgs(path)); err == nil || out.Len() != 0 {
		t.Fatal("racing destination overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "other process" {
		t.Fatal("destination changed", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal("temporary file leaked", err)
	}
}

func TestGuidedDownloadMediaChooser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chosen.bin")
	a, api, _, diag, _ := guidedApp(t, "1\n3\n"+path+"\n")
	api.history = []*line.Message{
		{ID: "100", ContentType: 0, Text: "excluded text"},
		{ID: "101", ContentType: 1, CreatedTime: "1700000000000"},
		{ID: "102", ContentType: 2, CreatedTime: "1700000000001"},
		{ID: "103", ContentType: 3, CreatedTime: "1700000000002"},
		{ID: "104", ContentType: 14, CreatedTime: "1700000000003", ContentMetadata: map[string]string{"FILE_NAME": "notes.txt"}},
		{ID: "105", ContentType: 7},
	}
	if err := a.Run([]string{"download"}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"[Image]", "[Video]", "[Audio]", "[File: notes.txt]", "2023-11-", "ID 103"} {
		if !strings.Contains(diag.String(), label) {
			t.Fatalf("chooser missing %q", label)
		}
	}
	if strings.Contains(diag.String(), "excluded text") || strings.Contains(diag.String(), "[Sticker]") {
		t.Fatal("ineligible messages shown")
	}
}

func TestGuidedDownloadEmptyAndDash(t *testing.T) {
	a, api, _, _, _ := guidedApp(t, "1\n")
	api.history = []*line.Message{{ID: "123", ContentType: 7}}
	if err := a.Run([]string{"download"}); err == nil || !strings.Contains(err.Error(), "no downloadable media") {
		t.Fatal("unclear empty state", err)
	}
	for _, terminal := range []bool{false, true} {
		a, _, out, _, _ := guidedApp(t, "1\n1\n-\n")
		a.StdoutIsTerminal = terminal
		if err := a.Run([]string{"download"}); err == nil || out.Len() != 0 {
			t.Fatal("guided dash bypassed stdout rules")
		}
	}
}

func (f *mediaDownloadAPI) GetRecentMessagesV2Context(_ context.Context, arg0 string, arg1 int) ([]*line.Message, error) {
	return f.GetRecentMessagesV2(arg0, arg1)
}
