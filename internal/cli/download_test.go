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
	message           *line.Message
	data              []byte
	err               error
	downloads         int
	albumPreviewCalls int
	albumOID          string
	albumChatID       string
	albumID           string
	onDownload        func()
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
func (f *mediaDownloadAPI) DownloadAlbumPreview(_ context.Context, oid, chatID, albumID string) ([]byte, error) {
	f.albumPreviewCalls++
	f.albumOID, f.albumChatID, f.albumID = oid, chatID, albumID
	if f.onDownload != nil {
		f.onDownload()
	}
	return f.data, f.err
}
func downloadApp(t *testing.T, kind int) (*App, *mediaDownloadAPI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	a, out, diag := testApp(nil)
	api := &mediaDownloadAPI{message: &line.Message{ID: "123", From: downloadChat, To: "u-self", ContentType: kind}, data: []byte{0, 1, 255, 254, 10, 13, 0}}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic"}}
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
	for _, msg := range api.history {
		msg.From, msg.To = "u-alice", "u-self"
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

func TestDownloadAlbumPreviewCLIAndValidation(t *testing.T) {
	const groupChat = "Caaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	albumMsg := func() *line.Message {
		return &line.Message{
			ID: "5002", From: "u-bob", To: groupChat, ToType: 2, ContentType: 16,
			ContentMetadata: map[string]string{
				"serviceType":   "AB",
				"albumName":     "Summit photos",
				"postEndUrl":    "line://group/home/albums/album?albumId=123&albumIdV2=abc-v2",
				"chatId":        groupChat,
				"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-2","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-3","mediaType":"I"}]`,
			},
		}
	}

	// --preview 0 rejected before lock/storage/temp-file creation
	t.Run("preview-zero-before-lock", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "thumb.jpg")
		a, _, out, _ := downloadApp(t, 16)
		a.Lock = func() (func(), error) { t.Fatal("--preview 0 opened session lock"); return nil, nil }
		err := a.Run([]string{"--error-format=json", "download", groupChat, "--message", "5002", "--preview", "0", "--output", outPath})
		var me *MachineError
		if !errors.As(err, &me) || me.Detail.Code != "invalid_arguments" || me.Detail.Stage != "validation" || out.Len() != 0 {
			t.Fatalf("unexpected error: %v", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatal("--preview 0 created a temp file")
		}
	})

	// Scripted --preview 2 with --json exact 3-field receipt
	t.Run("scripted-preview-json", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "summit-2.jpg")
		a, api, out, _ := downloadApp(t, 16)
		api.message = albumMsg()
		api.data = []byte("thumb-2-bytes")
		if err := a.Run([]string{"download", groupChat, "--message", "5002", "--preview", "2", "--output", path, "--json"}); err != nil {
			t.Fatal(err)
		}
		if api.albumPreviewCalls != 1 || api.albumOID != "oid-2" || api.albumChatID != groupChat || api.albumID != "abc-v2" {
			t.Fatalf("wrong album call: calls=%d oid=%q chat=%q album=%q", api.albumPreviewCalls, api.albumOID, api.albumChatID, api.albumID)
		}
		var receipt map[string]any
		if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || len(receipt) != 3 || receipt["path"] != path || receipt["message_id"] != "5002" || receipt["bytes"] != float64(len(api.data)) {
			t.Fatalf("receipt changed: %s", out.String())
		}
		saved, err := os.ReadFile(path)
		if err != nil || string(saved) != "thumb-2-bytes" {
			t.Fatalf("saved bytes = %q, %v", saved, err)
		}
	})

	// Scripted --output - binary stdout & oversized failure leaves nothing
	t.Run("scripted-stdout-and-oversized-failure", func(t *testing.T) {
		a, api, out, diag := downloadApp(t, 16)
		api.message = albumMsg()
		api.data = []byte{0xff, 0xd8, 0xff, 0x00, 0x0a}
		if err := a.Run([]string{"download", groupChat, "--message", "5002", "--preview", "3", "--output", "-"}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), api.data) || diag.Len() != 0 || api.albumOID != "oid-3" {
			t.Fatalf("stdout mismatch: out=%x diag=%q oid=%q", out.Bytes(), diag.String(), api.albumOID)
		}

		// Oversized error publishes neither stdout nor destination file
		for _, mode := range []string{"-", filepath.Join(t.TempDir(), "nope.jpg")} {
			a2, api2, out2, _ := downloadApp(t, 16)
			api2.message = albumMsg()
			api2.err = line.ErrOBSSizeLimit
			err := a2.Run([]string{"download", groupChat, "--message", "5002", "--output", mode})
			if err == nil || out2.Len() != 0 {
				t.Fatalf("mode %s: err=%v out=%d", mode, err, out2.Len())
			}
			if mode != "-" {
				if _, statErr := os.Lstat(mode); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("oversized download created %s", mode)
				}
			}
		}
	})

	// Error codes: out-of-range, explicit --preview on non-album, note/0-preview album
	t.Run("error-codes", func(t *testing.T) {
		cases := []struct {
			name     string
			msg      *line.Message
			args     []string
			wantCode string
		}{
			{
				name:     "out-of-range",
				msg:      albumMsg(),
				args:     []string{"--error-format=json", "download", groupChat, "--message", "5002", "--preview", "4", "--output", filepath.Join(t.TempDir(), "x.jpg")},
				wantCode: "invalid_arguments",
			},
			{
				name:     "preview-on-image",
				msg:      &line.Message{ID: "5002", From: "u-bob", To: groupChat, ToType: 2, ContentType: 1},
				args:     []string{"--error-format=json", "download", groupChat, "--message", "5002", "--preview", "1", "--output", filepath.Join(t.TempDir(), "x.jpg")},
				wantCode: "invalid_arguments",
			},
			{
				name:     "note-unsupported",
				msg:      &line.Message{ID: "5002", From: "u-bob", To: groupChat, ToType: 2, ContentType: 16, ContentMetadata: map[string]string{"serviceType": "GB", "text": "hello"}},
				args:     []string{"--error-format=json", "download", groupChat, "--message", "5002", "--output", filepath.Join(t.TempDir(), "x.jpg")},
				wantCode: "unsupported_content",
			},
			{
				name:     "cross-chat-album-unsupported",
				msg:      &line.Message{ID: "5002", From: "u-bob", To: groupChat, ToType: 2, ContentType: 16, ContentMetadata: map[string]string{"serviceType": "AB", "chatId": "c-other", "previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"}]`}},
				args:     []string{"--error-format=json", "download", groupChat, "--message", "5002", "--output", filepath.Join(t.TempDir(), "x.jpg")},
				wantCode: "unsupported_content",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				a, api, _, _ := downloadApp(t, 16)
				api.message = tc.msg
				err := a.Run(tc.args)
				var me *MachineError
				if !errors.As(err, &me) || me.Detail.Code != tc.wantCode {
					t.Fatalf("got err=%v, want code %s", err, tc.wantCode)
				}
				if api.albumPreviewCalls != 0 || api.downloads != 0 {
					t.Fatalf("unexpected download calls: album=%d obs=%d", api.albumPreviewCalls, api.downloads)
				}
			})
		}
	})
}

func TestGuidedDownloadAlbumChooserAndPreviewPrompt(t *testing.T) {
	history := func() []*line.Message {
		return []*line.Message{
			{ID: "100", From: "u-alice", To: "u-self", ContentType: 16, ContentMetadata: map[string]string{"serviceType": "GB", "text": "Excluded note"}},
			{ID: "101", From: "u-alice", To: "u-self", ContentType: 16, ContentMetadata: map[string]string{"serviceType": "AB", "albumName": "Empty album", "chatId": "u-alice", "previewMedias": `[]`}},
			{ID: "102", From: "u-alice", To: "u-self", ContentType: 16, ContentMetadata: map[string]string{"serviceType": "AB", "albumName": "Cross chat album", "chatId": "c-other", "previewMedias": `[{"svc":"album","sid":"a","mediaOid":"x","mediaType":"I"}]`}},
			{ID: "103", From: "u-alice", To: "u-self", ContentType: 16, CreatedTime: "1795673700000", ContentMetadata: map[string]string{
				"serviceType":   "AB",
				"albumName":     "Summit photos",
				"postEndUrl":    "line://group/home/albums/album?albumIdV2=v2-guided",
				"chatId":        "u-alice",
				"previewMedias": `[{"svc":"album","sid":"a","mediaOid":"oid-1","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-2","mediaType":"I"},{"svc":"album","sid":"a","mediaOid":"oid-3","mediaType":"I"}]`,
			}},
		}
	}

	// Explicit preview number "2"
	path1 := filepath.Join(t.TempDir(), "p2.jpg")
	a, api, _, diag, _ := guidedApp(t, "1\n1\n2\n"+path1+"\n")
	api.history = history()
	if err := a.Run([]string{"download"}); err != nil {
		t.Fatal(err)
	}
	if api.albumPreviewCalls != 1 || api.albumOID != "oid-2" || api.albumID != "v2-guided" {
		t.Fatalf("guided album download: calls=%d oid=%q album=%q", api.albumPreviewCalls, api.albumOID, api.albumID)
	}
	if !strings.Contains(diag.String(), "[Album: Summit photos] (3 previews)") || !strings.Contains(diag.String(), "Preview number (1-3) [1]: ") {
		t.Fatalf("missing album chooser/prompt in:\n%s", diag.String())
	}
	if strings.Contains(diag.String(), "Excluded note") || strings.Contains(diag.String(), "Empty album") || strings.Contains(diag.String(), "Cross chat album") {
		t.Fatalf("ineligible post appeared in chooser:\n%s", diag.String())
	}

	// Pressing Enter defaults to preview 1
	path2 := filepath.Join(t.TempDir(), "p1.jpg")
	a2, api2, _, _, _ := guidedApp(t, "1\n1\n\n"+path2+"\n")
	api2.history = history()
	if err := a2.Run([]string{"download"}); err != nil {
		t.Fatal(err)
	}
	if api2.albumOID != "oid-1" {
		t.Fatalf("empty enter did not default to oid-1: got %q", api2.albumOID)
	}

	// Explicit --message ID prompts only for output, not preview number
	path3 := filepath.Join(t.TempDir(), "p1-direct.jpg")
	a3, api3, _, diag3, _ := guidedApp(t, path3+"\n")
	api3.history = history()
	if err := a3.Run([]string{"download", "id:u-alice", "--message", "103"}); err != nil {
		t.Fatal(err)
	}
	if api3.albumOID != "oid-1" || strings.Contains(diag3.String(), "Preview number") {
		t.Fatalf("explicit --message unexpectedly prompted for preview: oid=%q diag=%q", api3.albumOID, diag3.String())
	}
}
