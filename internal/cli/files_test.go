package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type downloadAPI struct{ session.API }

func (*downloadAPI) GetRecentMessagesV2(string, int) ([]*line.Message, error) {
	return []*line.Message{{ID: "123", ContentType: 14}}, nil
}
func (*downloadAPI) DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error) {
	return []byte("downloaded file"), nil
}

func TestDownloadPublishesFileAndNeverOverwrites(t *testing.T) {
	a, out, _ := testApp(nil)
	a.Manager.NewClient = func(string) session.API { return &downloadAPI{} }
	path := filepath.Join(t.TempDir(), "output.bin")
	args := []string{"download", "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--message", "123", "--output", path, "--json"}
	if err := a.Run(args); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "downloaded file" || out.Len() == 0 {
		t.Fatal("bad output file", err)
	}
	a.Lock = func() (func(), error) { t.Fatal("existing output reached network/session"); return nil, nil }
	if err := a.Run(args); err == nil {
		t.Fatal("overwrote existing file")
	}
}

func TestFileArgumentsValidateBeforeSession(t *testing.T) {
	for _, args := range [][]string{
		{"send", "Alice", "--file", "missing.bin"},
		{"send", "Alice", "--file", "missing.bin", "--text", "test"},
		{"send", "Alice", "--file", "missing.bin", "--stdin"},
		{"download", "--help"}, {"download", "Alice", "--message", "123"},
	} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("session opened before validating file options"); return nil, nil }
		_ = a.Run(args)
	}
}
