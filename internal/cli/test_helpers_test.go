package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// Shared synthetic fixtures for package tests.
type fakeLoginStorage struct {
	initiallyEmptyStore
	consent, accepted, closed bool
	prepares                  int
	err                       error
	identity                  string
}

func (s *fakeLoginStorage) Prepare() error {
	s.prepares++
	if s.err != nil {
		return s.err
	}
	if s.consent && !s.accepted {
		return session.ErrHostConsent
	}
	return nil
}

func (s *fakeLoginStorage) StorageIdentity() (string, error) { return s.identity, nil }

func (s *fakeLoginStorage) RequiresHostConsent() bool { return s.consent }

func (s *fakeLoginStorage) AcceptHost() { s.accepted = true }

func (s *fakeLoginStorage) Close() { s.closed = true }

type chatAPI struct {
	messageAPI
	boxes     []line.MessageBox
	names     map[string]string
	options   []line.MessageBoxesOptions
	batches   []int
	nameError error
}

func (f *chatAPI) GetAllContactIds() ([]string, error) { return nil, nil }

func (f *chatAPI) GetMessageBoxes(options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	f.options = append(f.options, options)
	return &line.MessageBoxesResponse{MessageBoxes: f.boxes}, nil
}

func (f *chatAPI) GetContactsV2(ids []string) (*line.ContactsResponse, error) {
	if f.nameError != nil {
		return nil, f.nameError
	}
	f.batches = append(f.batches, len(ids))
	result := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
	for _, id := range ids {
		result.Contacts[id] = line.ContactWrapper{Contact: line.Contact{DisplayName: "Original", DisplayNameOverridden: f.names[id]}}
	}
	return result, nil
}

func (f *chatAPI) GetChats(ids []string, members, invitees bool) (*line.GetChatsResponse, error) {
	if len(ids) > 20 {
		return nil, errors.New("group lookup batch exceeds the verified API limit")
	}
	if members || invitees {
		return nil, errors.New("unnecessary membership lookup")
	}
	if f.nameError != nil {
		return nil, f.nameError
	}
	f.batches = append(f.batches, len(ids))
	result := &line.GetChatsResponse{}
	for _, id := range ids {
		result.Chats = append(result.Chats, line.Chat{ChatMid: id, ChatName: f.names[id]})
	}
	return result, nil
}

func (f *chatAPI) GetAllContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetAllContactIds()
}

func (f *chatAPI) GetMessageBoxesContext(_ context.Context, options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	return f.GetMessageBoxes(options)
}

func (f *chatAPI) GetContactsV2Context(_ context.Context, ids []string) (*line.ContactsResponse, error) {
	return f.GetContactsV2(ids)
}

func (f *chatAPI) GetChatsContext(_ context.Context, ids []string, members, invitees bool) (*line.GetChatsResponse, error) {
	return f.GetChats(ids, members, invitees)
}

type testStore struct{ state *session.State }

func (s *testStore) Load() (*session.State, error) { return s.state, nil }

func (s *testStore) Save(state *session.State) error { s.state = state; return nil }

func (s *testStore) Delete() error { s.state = nil; return nil }

type readAPI struct {
	session.API
	pages      []*line.MessageBoxesResponse
	cursors    []string
	ids        []string
	batchSizes []int
}

func (f *readAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u1", DisplayName: "Name\x1b[2J\n注入"}, nil
}

func (f *readAPI) GetMessageBoxes(options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	page := f.pages[len(f.cursors)]
	f.cursors = append(f.cursors, options.MinChatID)
	return page, nil
}

func (f *readAPI) GetAllContactIds() ([]string, error) { return f.ids, nil }

func (f *readAPI) GetContactsV2(ids []string) (*line.ContactsResponse, error) {
	f.batchSizes = append(f.batchSizes, len(ids))
	response := &line.ContactsResponse{Contacts: make(map[string]line.ContactWrapper)}
	for _, id := range ids {
		response.Contacts[id] = line.ContactWrapper{Contact: line.Contact{DisplayName: id}}
	}
	return response, nil
}

func testApp(api *readAPI) (*App, *bytes.Buffer, *bytes.Buffer) {
	out, diagnostics := new(bytes.Buffer), new(bytes.Buffer)
	m := session.NewManager(&testStore{state: &session.State{AccessToken: "secret"}})
	m.NewClient = func(string) session.API { return api }
	return &App{Out: out, Err: diagnostics, Manager: m, Version: "test", Lock: func() (func(), error) { return func() {}, nil }}, out, diagnostics
}

func (f *readAPI) GetProfileContext(context.Context) (*line.Profile, error) { return f.GetProfile() }

func (f *readAPI) GetMessageBoxesContext(_ context.Context, options line.MessageBoxesOptions) (*line.MessageBoxesResponse, error) {
	return f.GetMessageBoxes(options)
}

func (f *readAPI) GetAllContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetAllContactIds()
}

func (f *readAPI) GetContactsV2Context(_ context.Context, ids []string) (*line.ContactsResponse, error) {
	return f.GetContactsV2(ids)
}

const syntheticQRURL = "line://au/q/synthetic-session?secret=synthetic-public-key&e2eeVersion=1"

func qrApp(t *testing.T, input string) (*App, *guidedAPI, *bytes.Buffer, *bytes.Buffer, *int, *bool) {
	t.Helper()
	a, api, out, diagnostics, locked := guidedApp(t, input)
	a.Manager.Store = &initiallyEmptyStore{}
	a.Password = func() (string, error) { return "synthetic", nil }
	a.RenderQR = func(value string, _ QRTerminal) (QRFrame, error) {
		if value != syntheticQRURL {
			t.Fatal("renderer received incorrect value")
		}
		return QRFrame{Text: "[synthetic QR]\n", Columns: 14, Rows: 1}, nil
	}
	a.QRTerminal = func() QRTerminal { return QRTerminal{Width: 80} }
	calls := 0
	a.LoginQR = func(ctx context.Context, notify func(session.QRLoginEvent) error) (*line.Profile, error) {
		calls++
		if !*locked {
			t.Fatal("QR login without session lock")
		}
		for _, event := range []session.QRLoginEvent{
			{Kind: session.QRLoginCode, URL: syntheticQRURL, Attempt: 1, ExpiresIn: time.Minute},
			{Kind: session.QRLoginScanned}, {Kind: session.QRLoginPIN, PIN: "001234"},
			{Kind: session.QRLoginPhoneAccepted}, {Kind: session.QRLoginApproved},
		} {
			if err := notify(event); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := a.Manager.Store.Save(&session.State{AccessToken: "synthetic-access", Certificate: "synthetic-certificate", Generation: "new", MID: "synthetic-mid", CertificateOrigin: "qr", ExportedKeys: map[string]string{"1": "synthetic-export"}}); err != nil {
			return nil, err
		}
		return &line.Profile{DisplayName: "Synthetic"}, nil
	}
	return a, api, out, diagnostics, &calls, locked
}

type messageAPI struct {
	session.API
	history []*line.Message
	sent    *line.Message
}

func (f *messageAPI) GetRecentMessagesV2(string, int) ([]*line.Message, error) { return f.history, nil }

func (f *messageAPI) GetBlockedContactIds() ([]string, error) { return nil, nil }

func (f *messageAPI) SendMessage(_ int64, msg *line.Message) (*line.Message, error) {
	f.sent = msg
	return &line.Message{ID: "message-id"}, nil
}

func (f *messageAPI) GetRecentMessagesV2Context(_ context.Context, arg0 string, arg1 int) ([]*line.Message, error) {
	return f.GetRecentMessagesV2(arg0, arg1)
}

func (f *messageAPI) GetBlockedContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetBlockedContactIds()
}

func (f *messageAPI) SendMessageContext(_ context.Context, arg0 int64, msg *line.Message) (*line.Message, error) {
	return f.SendMessage(arg0, msg)
}

type guidedAPI struct {
	chatAPI
	sends, reactions, removals, unsends, logins int
}

func (f *guidedAPI) GetAllContactIds() ([]string, error) { return []string{"u-alice", "u-new"}, nil }

func (f *guidedAPI) GetProfile() (*line.Profile, error) {
	return &line.Profile{Mid: "u-self", DisplayName: "Alex"}, nil
}

func (f *guidedAPI) SendMessage(_ int64, m *line.Message) (*line.Message, error) {
	f.sends++
	f.sent = m
	return &line.Message{ID: "999"}, nil
}

func (f *guidedAPI) React(_ int64, _ string, _ line.ReactionType) error { f.reactions++; return nil }

func (f *guidedAPI) CancelReaction(_ int64, _ string) error { f.removals++; return nil }

func (f *guidedAPI) UnsendMessage(_ int64, _ string) error { f.unsends++; return nil }

func (f *guidedAPI) DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error) {
	return []byte("file bytes"), nil
}

func (f *guidedAPI) Login(email, password, certificate string) (*line.LoginResult, error) {
	f.logins++
	if email != "you@example.com" || password != "synthetic" {
		return nil, errors.New("unexpected credentials")
	}
	return &line.LoginResult{AuthToken: "synthetic-token", NoE2EE: true}, nil
}

type checkedInput struct {
	t      *testing.T
	locked *bool
	io.Reader
	onRead func()
}

func (r checkedInput) Read(p []byte) (int, error) {
	r.t.Helper()
	if *r.locked {
		r.t.Fatal("prompt held the session lock")
	}
	if r.onRead != nil {
		r.onRead()
	}
	return r.Reader.Read(p)
}

func guidedApp(t *testing.T, input string) (*App, *guidedAPI, *bytes.Buffer, *bytes.Buffer, *bool) {
	t.Helper()
	f := &guidedAPI{chatAPI: chatAPI{names: map[string]string{"u-alice": "Alice", "u-new": "New contact"}, boxes: []line.MessageBox{{ID: "u-alice"}}}}
	f.history = []*line.Message{{ID: "123", From: "u-self", To: "u-alice", Text: "My message"}, {ID: "124", From: "u-alice", To: "u-self", Text: "Their message"}, {ID: "125", From: "u-alice", ContentType: 14, ContentMetadata: map[string]string{"FILE_NAME": "notes.txt"}}}
	a, out, diagnostics := testApp(nil)
	a.Interactive = true
	a.Manager.Store = &testStore{state: &session.State{Version: 1, MID: "u-self", AccessToken: "synthetic-token", Generation: "one", NoE2EE: true}}
	a.Manager.NewClient = func(string) session.API { return f }
	locked := false
	a.Lock = func() (func(), error) {
		if locked {
			t.Fatal("nested lock")
		}
		locked = true
		return func() { locked = false }, nil
	}
	a.In = checkedInput{t: t, locked: &locked, Reader: strings.NewReader(input)}
	return a, f, out, diagnostics, &locked
}

func (f *guidedAPI) GetProfileContext(context.Context) (*line.Profile, error) { return f.GetProfile() }

func (f *guidedAPI) GetAllContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetAllContactIds()
}

func (f *guidedAPI) SendMessageContext(_ context.Context, arg0 int64, m *line.Message) (*line.Message, error) {
	return f.SendMessage(arg0, m)
}

func (f *guidedAPI) ReactContext(_ context.Context, arg0 int64, arg1 string, arg2 line.ReactionType) error {
	return f.React(arg0, arg1, arg2)
}

func (f *guidedAPI) CancelReactionContext(_ context.Context, arg0 int64, arg1 string) error {
	return f.CancelReaction(arg0, arg1)
}

func (f *guidedAPI) UnsendMessageContext(_ context.Context, arg0 int64, arg1 string) error {
	return f.UnsendMessage(arg0, arg1)
}

type initiallyEmptyStore struct{ testStore }

func (s *initiallyEmptyStore) Load() (*session.State, error) {
	if s.state == nil {
		return nil, session.ErrNotFound
	}
	return s.testStore.Load()
}
