package cli

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestReadValidationNeverOpensSession(t *testing.T) {
	for _, args := range [][]string{
		{"read", "--help"}, {"read"}, {"read", "--json"}, {"read", "id:bad id"}, {"read", "Alice", "--message", "abc"},
		{"read", "Alice", "--message", "local-1"}, {"read", "Alice", "extra"}, {"read", "Alice", "--unknown"},
		{"read", "Alice", "--timeout", "-1s"},
	} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened session for invalid arguments"); return nil, nil }
		_ = a.Run(args)
	}
}

func TestReadExplicitHumanAndJSON(t *testing.T) {
	a, f, out, _, _ := guidedApp(t, "")
	a.Interactive = false
	if err := a.Run([]string{"read", "id:u-alice"}); err != nil {
		t.Fatal(err)
	}
	if f.reads != 1 || f.readChat != "u-alice" || f.readMessage != "125" || f.readSeq == 0 {
		t.Fatalf("receipt %+v", f)
	}
	if got := out.String(); !strings.Contains(got, "Marked as read in u-alice up to the newest message from others.") || !strings.Contains(got, `"Read"`) || strings.Contains(got, "125") {
		t.Fatalf("human receipt %q", got)
	}
	out.Reset()
	if err := a.Run([]string{"read", "id:u-alice", "--message", "124", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 4 || result["action"] != "read" || result["chat_id"] != "u-alice" || result["message_id"] != "124" || result["request_sequence"] != float64(f.readSeq) || f.reads != 2 {
		t.Fatalf("JSON %v", result)
	}
	out.Reset()
	if err := a.Run([]string{"read", "id:u-alice", "--message", "124", "--show-ids"}); err != nil || !strings.Contains(out.String(), "up to the chosen message") || !strings.Contains(out.String(), "ID: 124") {
		t.Fatalf("show IDs %q %v", out.String(), err)
	}
}

func TestReadGuidedListsUnreadFirstAndConfirms(t *testing.T) {
	a, f, out, diagnostics, _ := guidedApp(t, "1\ny\n")
	f.boxes = []line.MessageBox{{ID: "u-new", LastMessages: []line.Message{{CreatedTime: "9000"}}}, {ID: "u-alice", UnreadCount: "2"}}
	if err := a.Run([]string{"read"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "1  Alice · Direct · 2 unread") || !strings.Contains(diagnostics.String(), `will see "Read"`) {
		t.Fatalf("picker %q", diagnostics.String())
	}
	if f.reads != 1 || f.readChat != "u-alice" || !strings.Contains(out.String(), "Marked as read in Alice") {
		t.Fatalf("guided receipt %+v %q", f, out.String())
	}
	a, f, _, _, _ = guidedApp(t, "1\nn\n")
	if err := a.Run([]string{"read"}); !errors.Is(err, ErrCancelled) || f.reads != 0 {
		t.Fatalf("declined confirmation sent a receipt: %v", err)
	}
}

func TestReadMachineErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		message string
		code    string
		outcome fault.Outcome
		reads   int
	}{
		{"unknown", io.ErrUnexpectedEOF, "", "remote_failed", fault.Unknown, 1},
		{"rejected", &line.ResponseError{Status: 401}, "", "authentication_failed", fault.Rejected, 1},
		{"not found", nil, "555", "message_not_found", fault.NotAttempted, 0},
		{"own message", nil, "123", "invalid_arguments", fault.NotAttempted, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f, out, _, _ := guidedApp(t, "")
			f.readErr = tc.err
			for i, m := range f.history {
				m.DeliveredTime = json.Number(strings.Repeat("1", i+1))
			}
			args := []string{"--error-format=json", "read", "id:u-alice", "--json"}
			if tc.message != "" {
				args = append(args, "--message", tc.message)
			}
			err := a.Run(args)
			var machine *MachineError
			if !errors.As(err, &machine) || machine.Detail.Code != tc.code || out.Len() != 0 || f.reads != tc.reads {
				t.Fatalf("got %+v reads=%d", err, f.reads)
			}
			op := machine.Detail.Mutation
			if op == nil || op.Action != "read" || op.ChatID != "u-alice" || op.Outcome != tc.outcome {
				t.Fatalf("mutation %+v", op)
			}
			if tc.reads == 1 && (op.RequestSequence == nil || *op.RequestSequence != f.readSeq || op.MessageID != "125") {
				t.Fatalf("attempt identity %+v", op)
			}
		})
	}
}

func TestReadDeadlineStopsBeforeReceipt(t *testing.T) {
	a, f, _, _, _ := guidedApp(t, "")
	a.Interactive = false
	var typed *fault.Error
	err := a.Run([]string{"--error-format=json", "read", "id:u-alice", "--timeout", "1ns"})
	var machine *MachineError
	if !errors.As(err, &machine) || machine.Detail.Code != "deadline_exceeded" || f.reads != 0 {
		t.Fatalf("deadline: %v %v", err, typed)
	}
}

// Read receipts are sent only by line read: no other production code path may
// reach sendChatChecked or MarkReadContext.
func TestOnlyReadCommandSendsReadReceipts(t *testing.T) {
	allowed := map[string]map[string]bool{
		"SendChatChecked":        {"pkg/line/methods.go": true},
		"SendChatCheckedContext": {"pkg/line/methods.go": true, "internal/session/manager.go": true, "internal/messaging/read.go": true},
		"MarkReadContext":        {"internal/messaging/read.go": true, "internal/cli/read.go": true},
	}
	root := filepath.Join("..", "..")
	seen := make(map[string]bool)
	for _, dir := range []string{"cmd", "internal", "pkg/line", "pkg/e2ee"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				var name string
				switch v := n.(type) {
				case *ast.SelectorExpr:
					name = v.Sel.Name
				case *ast.Field:
					if len(v.Names) > 0 {
						name = v.Names[0].Name
					}
				case *ast.FuncDecl:
					name = v.Name.Name
				}
				if files, ok := allowed[name]; ok {
					seen[name] = true
					if !files[rel] {
						t.Errorf("%s references %s; only line read may send read receipts", rel, name)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for name := range allowed {
		if !seen[name] {
			t.Fatalf("scan did not find %s; update this test", name)
		}
	}
}

// An empty older page ends available history for bounded lookups.
func (f *guidedAPI) GetPreviousMessagesV2Context(context.Context, string, line.MessageIDWrapper, int) ([]*line.Message, error) {
	return nil, nil
}
