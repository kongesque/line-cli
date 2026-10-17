package messaging

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// Shared synthetic fixtures for package tests.
var testPublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

type memStore struct {
	state   *session.State
	saveErr error
}

func (s *memStore) Load() (*session.State, error) {
	data, _ := json.Marshal(s.state)
	var copy session.State
	_ = json.Unmarshal(data, &copy)
	return &copy, nil
}

func (s *memStore) Save(state *session.State) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.state = state
	return nil
}

func (s *memStore) Delete() error { return nil }

type fakeAPI struct {
	session.API
	history           []*line.Message
	blocked           []string
	blockErr          error
	negotiateErr      error
	key               *line.E2EEPublicKey
	lookupIDs         []int
	group             *line.E2EEGroupSharedKey
	groupErr          error
	groupIDs          []int
	chats             *line.GetChatsResponse
	registrations     int
	registeredMembers []string
	registeredKeys    []int
	registeredWrapped []string
	sends             int
	sent              *line.Message
	sequence          int64
	sendErr           error
	store             *memStore
}

func (f *fakeAPI) GetRecentMessagesV2(string, int) ([]*line.Message, error) { return f.history, nil }

func (f *fakeAPI) GetBlockedContactIds() ([]string, error) { return f.blocked, f.blockErr }

func (f *fakeAPI) NegotiateE2EEPublicKey(string) (*line.E2EEPublicKey, error) {
	return f.key, f.negotiateErr
}

func (f *fakeAPI) GetE2EEPublicKey(_ string, _ int, id int) (*line.E2EEPublicKey, error) {
	f.lookupIDs = append(f.lookupIDs, id)
	return f.key, nil
}

func (f *fakeAPI) GetE2EEGroupSharedKey(_ string, id int) (*line.E2EEGroupSharedKey, error) {
	f.groupIDs = append(f.groupIDs, id)
	return f.group, f.groupErr
}

func (f *fakeAPI) GetLastE2EEGroupSharedKey(string) (*line.E2EEGroupSharedKey, error) {
	return f.group, f.groupErr
}

func (f *fakeAPI) GetChats([]string, bool, bool) (*line.GetChatsResponse, error) { return f.chats, nil }

func (f *fakeAPI) RegisterE2EEGroupKey(_ int, _ string, members []string, keys []int, wrapped []string) error {
	f.registrations++
	f.registeredMembers, f.registeredKeys, f.registeredWrapped = members, keys, wrapped
	f.groupErr = nil
	return nil
}

func (f *fakeAPI) SendMessage(seq int64, msg *line.Message) (*line.Message, error) {
	f.sends++
	f.sequence, f.sent = seq, msg
	if f.store.state.LastReqSeq != seq {
		panic("send before sequence persistence")
	}
	return &line.Message{ID: "server-id"}, f.sendErr
}

type fakeCrypto struct {
	Crypto
	payload      string
	decryptErr   error
	encryptErr   error
	loadErr      error
	loaded       map[string]string
	registered   []int
	unwrapped    []int
	decryptCalls int
	groupVersion int
}

func (f *fakeCrypto) LoadMyKeyFromExportedMap(keys map[string]string) error {
	f.loaded = keys
	return f.loadErr
}

func (f *fakeCrypto) MyKeyIDs() (int, int, error) { return 11, 111, nil }

func (f *fakeCrypto) MyPublicKey() (int, string, error) { return 11, testPublicKey, nil }

func (f *fakeCrypto) IsMyKey(id int) bool { return id == 11 || id == 10 }

func (f *fakeCrypto) HasPeerPublicKey(int) bool { return false }

func (f *fakeCrypto) RegisterPeerPublicKey(id int, _ string) { f.registered = append(f.registered, id) }

func (f *fakeCrypto) UnwrapGroupSharedKey(_ string, key *line.E2EEGroupSharedKey) (int, error) {
	f.unwrapped = append(f.unwrapped, key.GroupKeyID)
	return 333, nil
}

func (f *fakeCrypto) DecryptMessageV2(*line.Message) (string, error) {
	f.decryptCalls++
	return f.payload, f.decryptErr
}

func (f *fakeCrypto) DecryptGroupMessage(*line.Message, string) (string, int, error) {
	f.decryptCalls++
	return f.payload, 33, f.decryptErr
}

func (f *fakeCrypto) EncryptMessageV2(string, string, int, string, int, int, int, string) ([]string, error) {
	return encryptedChunks(11, 22), f.encryptErr
}

func (f *fakeCrypto) EncryptGroupMessage(string, string, string) ([]string, error) {
	return encryptedChunks(11, 33), f.encryptErr
}

func (f *fakeCrypto) EncryptGroupMessageVersion(chat, from string, version int, text string) ([]string, error) {
	f.groupVersion = version
	return f.EncryptGroupMessage(chat, from, text)
}

func (f *fakeCrypto) GenerateGroupKey() (int, error) { return 33, nil }

func (f *fakeCrypto) WrapGroupKeyForMember(string, int) (string, error) { return "wrapped", nil }

func encryptedChunks(sender, receiver int) []string {
	encode := func(id int) string {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(id))
		return base64.StdEncoding.EncodeToString(b)
	}
	return []string{"header", "ciphertext", "tag", encode(sender), encode(receiver)}
}

func setup(t *testing.T, noE2EE bool) (*Client, *fakeAPI, *fakeCrypto, *memStore) {
	t.Helper()
	s := &memStore{state: &session.State{Version: 1, MID: "u-self", AccessToken: "secret", NoE2EE: noE2EE, ExportedKeys: map[string]string{"11": "export"}}}
	f := &fakeAPI{key: &line.E2EEPublicKey{KeyID: "22", PublicKey: testPublicKey}, store: s}
	crypto := &fakeCrypto{payload: `{"text":"decrypted hello"}`}
	m := session.NewManager(s)
	m.NewClient = func(string) session.API { return f }
	m.Now = func() time.Time { return time.UnixMilli(12345) }
	c, err := newClient(m, func() (Crypto, error) { return crypto, nil })
	if err != nil {
		t.Fatal(err)
	}
	return c, f, crypto, s
}

func (f *fakeAPI) GetRecentMessagesV2Context(_ context.Context, arg0 string, arg1 int) ([]*line.Message, error) {
	return f.GetRecentMessagesV2(arg0, arg1)
}

func (f *fakeAPI) GetBlockedContactIdsContext(_ context.Context) ([]string, error) {
	return f.GetBlockedContactIds()
}

func (f *fakeAPI) NegotiateE2EEPublicKeyContext(_ context.Context, arg0 string) (*line.E2EEPublicKey, error) {
	return f.NegotiateE2EEPublicKey(arg0)
}

func (f *fakeAPI) GetE2EEPublicKeyContext(_ context.Context, arg0 string, arg1 int, id int) (*line.E2EEPublicKey, error) {
	return f.GetE2EEPublicKey(arg0, arg1, id)
}

func (f *fakeAPI) GetE2EEGroupSharedKeyContext(_ context.Context, arg0 string, id int) (*line.E2EEGroupSharedKey, error) {
	return f.GetE2EEGroupSharedKey(arg0, id)
}

func (f *fakeAPI) GetLastE2EEGroupSharedKeyContext(_ context.Context, arg0 string) (*line.E2EEGroupSharedKey, error) {
	return f.GetLastE2EEGroupSharedKey(arg0)
}

func (f *fakeAPI) GetChatsContext(_ context.Context, arg0 []string, arg1 bool, arg2 bool) (*line.GetChatsResponse, error) {
	return f.GetChats(arg0, arg1, arg2)
}

func (f *fakeAPI) RegisterE2EEGroupKeyContext(_ context.Context, arg0 int, arg1 string, members []string, keys []int, wrapped []string) error {
	return f.RegisterE2EEGroupKey(arg0, arg1, members, keys, wrapped)
}

func (f *fakeAPI) SendMessageContext(_ context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	return f.SendMessage(seq, msg)
}

type fileAPI struct {
	*fakeAPI
	order                            []string
	uploaded                         []byte
	data                             []byte
	options                          line.OBSDownloadOptions
	requestSID, requestOID, talkMeta string
	uploadErr, plainErr              error
	downloadErr                      error
	downloads                        int
}

func (f *fileAPI) UploadOBSWithSID(data []byte, sid string) (string, error) {
	f.order = append(f.order, "upload")
	f.uploaded = append([]byte(nil), data...)
	if sid != "emf" {
		panic("wrong file SID")
	}
	return "object-id", f.uploadErr
}

func (f *fileAPI) UploadOBSPlain(data []byte, oid, kind string) error {
	f.order = append(f.order, "plain_upload")
	f.uploaded = append([]byte(nil), data...)
	if oid != "server-id" || kind != "file" {
		panic("wrong post-send upload target")
	}
	return f.plainErr
}

func (f *fileAPI) SendMessage(seq int64, msg *line.Message) (*line.Message, error) {
	f.order = append(f.order, "send")
	return f.fakeAPI.SendMessage(seq, msg)
}

func (f *fileAPI) DownloadOBSWithSIDOptions(_ context.Context, oid, message, sid string, opts line.OBSDownloadOptions) ([]byte, error) {
	f.downloads++
	f.options = opts
	f.requestOID, f.talkMeta, f.requestSID = oid, message, sid
	return f.data, f.downloadErr
}

func (f *fileAPI) UploadOBSWithSIDContext(_ context.Context, data []byte, sid string) (string, error) {
	return f.UploadOBSWithSID(data, sid)
}

func (f *fileAPI) UploadOBSPlainContext(_ context.Context, data []byte, oid, kind string) error {
	return f.UploadOBSPlain(data, oid, kind)
}

func (f *fileAPI) SendMessageContext(_ context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	return f.SendMessage(seq, msg)
}

func (f *fakeAPI) GetPreviousMessagesV2Context(context.Context, string, line.MessageIDWrapper, int) ([]*line.Message, error) {
	return nil, nil
}
