package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/messaging"
	"github.com/kongesque/line-cli/internal/session"
)

type imageSendAPI struct {
	messageAPI
	uploads int
	kind    string
	data    []byte
	err     error
}

func (f *imageSendAPI) UploadOBSPlainContext(_ context.Context, data []byte, id, kind string) error {
	if id != "message-id" {
		panic("wrong image message id")
	}
	f.uploads++
	f.kind = kind
	f.data = append([]byte(nil), data...)
	return f.err
}
func cliImageFixture(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestImageArgumentsBeforeSession(t *testing.T) {
	path := cliImageFixture(t)
	bad := filepath.Join(t.TempDir(), "bad.png")
	if err := os.WriteFile(bad, []byte("invalid image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, options := range [][]string{
		{"--image", ""}, {"--image", "missing"}, {"--image", bad}, {"--image", t.TempDir()},
		{"--image", path, "--text", "caption"}, {"--image", path, "--stdin"}, {"--image", path, "--file", path},
		{"--image", path, "--mentions", "[]"}, {"--image", path, "--reply-to", "123"},
	} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			a, _, _ := testApp(nil)
			a.Lock = func() (func(), error) { t.Fatal("image validation accessed session"); return nil, nil }
			if err := a.Run(append([]string{"send", "Alice"}, options...)); err == nil {
				t.Fatal("accepted invalid image options")
			}
		})
	}
}
func TestImageAndGenericFileRemainDistinct(t *testing.T) {
	path := cliImageFixture(t)
	for _, flag := range []string{"--image", "--file"} {
		a, out, diagnostics := testApp(nil)
		api := &imageSendAPI{}
		a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
		a.Manager.NewClient = func(string) session.API { return api }
		if err := a.Run([]string{"send", "id:u-peer", "--allow-plaintext", flag, path, "--json"}); err != nil {
			t.Fatal(err)
		}
		var result messaging.SendResult
		if json.Unmarshal(out.Bytes(), &result) != nil || result.ID != "message-id" || result.Encrypted || result.ChatID != "u-peer" || api.uploads != 1 || diagnostics.Len() != 0 {
			t.Fatal("incorrect image/file receipt")
		}
		_, format, err := image.DecodeConfig(bytes.NewReader(api.data))
		if err != nil {
			t.Fatal(err)
		}
		if flag == "--image" {
			if api.sent.ContentType != 1 || api.kind != "image" || format != "jpeg" {
				t.Fatal("image sent as generic file")
			}
		} else if api.sent.ContentType != 14 || api.kind != "file" || format != "png" {
			t.Fatal("generic file changed")
		}
	}
}
func TestPlainImageUploadFailurePreservesJSONReceipt(t *testing.T) {
	path := cliImageFixture(t)
	a, out, diagnostics := testApp(nil)
	api := &imageSendAPI{err: errors.New("private raw body")}
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return api }
	err := a.Run([]string{"--error-format=json", "send", "id:u-peer", "--allow-plaintext", "--image", path, "--json"})
	if err == nil || out.Len() != 0 || api.uploads != 1 {
		t.Fatal("incorrect failed-upload result")
	}
	detail := machineDetail(t, err)
	body, marshalErr := json.Marshal(detail)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if diagnostics.Len() != 0 || strings.Contains(string(body), "private raw body") || !strings.Contains(string(body), `"message_id":"message-id"`) || !strings.Contains(string(body), `"outcome":"confirmed"`) {
		t.Fatal("missing or unsafe confirmed image receipt", string(body))
	}
}
