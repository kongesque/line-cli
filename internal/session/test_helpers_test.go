package session

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

// Shared synthetic fixtures for package tests.
// Fixtures describe header layouts only; they are not authentic systemd blobs.
func fixture(id string) []byte {
	b := make([]byte, 48)
	idBytes, _ := hex.DecodeString(id)
	copy(b, idBytes)
	for i, v := range []uint32{32, 1, 12, 16} {
		binary.LittleEndian.PutUint32(b[16+i*4:], v)
	}
	if id == tpmUserID || id == pkUserID {
		h := make([]byte, 56)
		binary.LittleEndian.PutUint64(h, 1<<7)
		binary.LittleEndian.PutUint16(h[8:], 11)
		binary.LittleEndian.PutUint16(h[10:], 0x23)
		binary.LittleEndian.PutUint32(h[12:], 4)
		binary.LittleEndian.PutUint32(h[16:], 32)
		b = append(b, h...)
		if id == pkUserID {
			h := make([]byte, 16)
			binary.LittleEndian.PutUint64(h, 1<<11)
			binary.LittleEndian.PutUint32(h[8:], 4)
			b = append(b, h...)
		}
	}
	h := make([]byte, 8+24+32+16)
	binary.LittleEndian.PutUint64(h, 7)
	return append(b, h...)
}

func encode(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func envelopeState() *State {
	revision := int64(172)
	return &State{Version: 1, MID: "synthetic-mid", AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh",
		Email: "synthetic@example.test", Certificate: "synthetic-cert", Generation: "synthetic-generation",
		RefreshAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), LastReqSeq: 73, WatchRevision: &revision,
		ExportedKeys: map[string]string{"key": "synthetic-letter-sealing-material"}}
}

type memoryStore struct {
	state   *State
	saves   int
	saveErr error
}

func (s *memoryStore) Load() (*State, error) {
	if s.state == nil {
		return nil, ErrNotFound
	}
	data, _ := json.Marshal(s.state)
	var copy State
	json.Unmarshal(data, &copy)
	return &copy, nil
}

func (s *memoryStore) Save(state *State) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves++
	data, _ := json.Marshal(state)
	s.state = new(State)
	return json.Unmarshal(data, s.state)
}

func (s *memoryStore) Delete() error { s.state = nil; return nil }

type fakeAPI struct {
	API
	loginResults  []*line.LoginResult
	loginCalls    int
	waitResult    *line.LoginResult
	waitNoE2EE    bool
	refreshResult *line.TokenV3IssueResult
	refreshErr    error
	refreshCalls  int
}

func (f *fakeAPI) Login(email, password, certificate string) (*line.LoginResult, error) {
	if certificate != "" {
		return nil, errors.New("unexpected certificate reuse")
	}
	res := f.loginResults[f.loginCalls]
	f.loginCalls++
	return res, nil
}

func (f *fakeAPI) WaitForLogin(verifier string, noE2EE bool) (*line.LoginResult, error) {
	f.waitNoE2EE = noE2EE
	return f.waitResult, nil
}

func (f *fakeAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u123", DisplayName: "Test"}, nil
}

func (f *fakeAPI) RefreshAccessToken(string) (*line.TokenV3IssueResult, error) {
	f.refreshCalls++
	return f.refreshResult, f.refreshErr
}

func testManager(store *memoryStore, api *fakeAPI) *Manager {
	m := NewManager(store)
	m.NewClient = func(string) API { return api }
	m.Now = func() time.Time { return time.Unix(1000, 0) }
	m.ExportKeys = func(context.Context, API, *line.LoginResult) (map[string]string, error) {
		return map[string]string{"123": "exported-key"}, nil
	}
	return m
}

func (f *fakeAPI) GetProfileContext(context.Context) (*line.Profile, error) { return f.GetProfile() }

func testSessionFiles(t *testing.T) *sessionFiles {
	t.Helper()
	f, err := openSessionFiles(filepath.Join(t.TempDir(), "line-cli"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.root.Close() })
	return f
}

type fakeSecretService struct {
	values map[string][]byte
	fault  func(action, account string) bool
}

func (s *fakeSecretService) helper() secretToolStore {
	return secretToolStore{run: func(args []string, input []byte) secretResult {
		action, account := args[0], args[len(args)-1]
		if s.fault != nil && s.fault(action, account) {
			return secretResult{code: 1, diagnostic: true}
		}
		switch action {
		case "store":
			s.values[account] = append([]byte(nil), input...)
		case "lookup":
			if value, ok := s.values[account]; ok {
				return secretResult{output: append([]byte(nil), value...)}
			}
			return secretResult{code: 1}
		case "search":
			if value, ok := s.values[account]; ok {
				return secretResult{output: append(append([]byte("[/synthetic]\nsecret = "), value...), '\n')}
			}
		case "clear":
			clear(s.values[account])
			delete(s.values, account)
		}
		return secretResult{}
	}}
}

func syntheticFileStore(t *testing.T) (secretFileStore, *fakeSecretService) {
	t.Helper()
	service := &fakeSecretService{values: map[string][]byte{}}
	return secretFileStore{path: filepath.Join(t.TempDir(), "line-cli", "session.enc"), secrets: service.helper()}, service
}
