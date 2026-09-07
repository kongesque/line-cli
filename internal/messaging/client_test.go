package messaging

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/highesttt/matrix-line-messenger/internal/session"
	"github.com/highesttt/matrix-line-messenger/pkg/line"
)

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
func (f *fakeAPI) GetBlockedContactIds() ([]string, error)                  { return f.blocked, f.blockErr }
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
}

func (f *fakeCrypto) LoadMyKeyFromExportedMap(keys map[string]string) error {
	f.loaded = keys
	return f.loadErr
}
func (f *fakeCrypto) MyKeyIDs() (int, int, error)            { return 11, 111, nil }
func (f *fakeCrypto) MyPublicKey() (int, string, error)      { return 11, testPublicKey, nil }
func (f *fakeCrypto) IsMyKey(id int) bool                    { return id == 11 || id == 10 }
func (f *fakeCrypto) HasPeerPublicKey(int) bool              { return false }
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
func (f *fakeCrypto) GenerateGroupKey() (int, error)                    { return 33, nil }
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

func TestHistoryDecryptsDMAndOldOwnDeviceEcho(t *testing.T) {
	c, f, crypto, _ := setup(t, false)
	f.history = []*line.Message{
		{ID: "incoming", From: "u-peer", To: "u-self", Chunks: encryptedChunks(22, 11)},
		{ID: "echo", From: "u-self", To: "u-peer", Chunks: encryptedChunks(10, 22)},
	}
	result, err := c.History("u-peer", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0].Text != "decrypted hello" || result[1].Status != "decrypted" || crypto.loaded["11"] != "export" {
		t.Fatal("failed to restore/decrypt")
	}
	for _, id := range f.lookupIDs {
		if id != 22 {
			t.Fatal("looked up wrong device key")
		}
	}
}

func TestHistoryFetchesHistoricalGroupKeyWithoutRegistration(t *testing.T) {
	c, f, crypto, _ := setup(t, false)
	f.history = []*line.Message{{From: "u-peer", To: "c-group", ToType: 2, Chunks: encryptedChunks(22, 33)}}
	f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
	result, err := c.History("c-group", 20)
	if err != nil || result[0].Status != "decrypted" || len(crypto.unwrapped) != 1 || f.groupIDs[0] != 33 || f.registrations != 0 {
		t.Fatalf("group read failed: %v", err)
	}
}

func TestHistoryNeverExposesCiphertextAsText(t *testing.T) {
	c, f, crypto, _ := setup(t, false)
	crypto.decryptErr = errors.New("raw-secret-response")
	f.history = []*line.Message{
		{From: "u-peer", To: "u-self", Text: "misleading fallback", Chunks: encryptedChunks(22, 11)},
		{Text: "also not plaintext", ContentMetadata: map[string]string{"e2eeVersion": "2"}},
		{ContentType: 1, Chunks: encryptedChunks(22, 11)},
		{Text: "plain hello"},
	}
	result, err := c.History("u-peer", 20)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "raw-secret-response") || strings.Contains(string(data), "misleading") || strings.Contains(string(data), "ciphertext") {
		t.Fatal("unsafe history output")
	}
	if result[0].Status != "decryption_failed" || result[1].Status != "decryption_failed" || result[2].Status != "unsupported" || result[3].Text != "plain hello" {
		t.Fatal("incorrect message status")
	}
}

func TestMismatchedPeerKeyStopsDecryption(t *testing.T) {
	c, f, crypto, _ := setup(t, false)
	f.key.KeyID = "99"
	f.history = []*line.Message{{From: "u-peer", To: "u-self", Chunks: encryptedChunks(22, 11)}}
	result, _ := c.History("u-peer", 1)
	if result[0].Status != "decryption_failed" || crypto.decryptCalls != 0 {
		t.Fatal("used mismatched key")
	}
}

func TestSendEncryptsAndPersistsMonotonicSequence(t *testing.T) {
	c, f, _, s := setup(t, false)
	first, err := c.Send("u-peer", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Encrypted || f.sent.Text != "" || len(f.sent.Chunks) != 5 || f.sent.ContentMetadata["e2eeVersion"] != "2" || f.sent.ToType != 0 {
		t.Fatal("invalid encrypted wire message")
	}
	// Construct another client to model the next CLI invocation at the same time.
	next, err := newClient(c.Session, func() (Crypto, error) { return &fakeCrypto{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	second, err := next.Send("u-peer", "hello again")
	if err != nil || second.RequestSequence != first.RequestSequence+1 || s.state.LastReqSeq != second.RequestSequence {
		t.Fatalf("sequence lost on restart: %v", err)
	}
}

func TestSendOnlyAllowsExplicitPlaintextFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noE2EE bool
		err    error
		sends  int
	}{
		{"account off", true, nil, 1},
		{"peer off", false, line.ErrE2EEDisabled, 1},
		{"malformed peer", false, line.ErrNoUsableE2EEPublicKey, 0},
		{"network failure", false, errors.New("network failure"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, _, _ := setup(t, tc.noE2EE)
			f.negotiateErr = tc.err
			result, err := c.Send("u-peer", "hello")
			if f.sends != tc.sends {
				t.Fatal("unsafe fallback")
			}
			if tc.sends == 1 && (err != nil || result.Encrypted || f.sent.Text != "hello" || len(f.sent.Chunks) != 0) {
				t.Fatal("incorrect plaintext send")
			}
			if tc.sends == 0 && err == nil {
				t.Fatal("missing failure")
			}
		})
	}
}

func TestSendStopsOnBlockedContactOrFailedBlockLookup(t *testing.T) {
	for _, lookupError := range []bool{false, true} {
		c, f, _, _ := setup(t, false)
		if lookupError {
			f.blockErr = errors.New("network")
		} else {
			f.blocked = []string{"u-peer"}
		}
		if _, err := c.Send("u-peer", "hello"); err == nil || f.sends != 0 {
			t.Fatal("bypassed block check")
		}
	}
}

func TestSendStopsOnCryptoOrPersistenceFailure(t *testing.T) {
	for _, persistence := range []bool{false, true} {
		c, f, crypto, s := setup(t, false)
		if persistence {
			s.saveErr = errors.New("keychain denied")
		} else {
			crypto.encryptErr = errors.New("crypto failed")
		}
		if _, err := c.Send("u-peer", "hello"); err == nil || f.sends != 0 {
			t.Fatal("sent after preparation failure")
		}
	}
}

func TestAmbiguousSendIsNotRetriedOrLeaked(t *testing.T) {
	c, f, _, s := setup(t, false)
	f.sendErr = errors.New("timeout raw-secret-response")
	_, err := c.Send("u-peer", "hello")
	if err == nil || f.sends != 1 || s.state.LastReqSeq == 0 || strings.Contains(err.Error(), "raw-secret-response") || !strings.Contains(err.Error(), "delivery may have occurred") {
		t.Fatal("unsafe send retry/error")
	}
}

func TestGroupSendRegistersCompleteMembershipIncludingSelf(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
	f.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-self": true, "u-peer": true}}}}}}
	result, err := c.Send("c-group", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Encrypted || f.registrations != 1 || strings.Join(f.registeredMembers, ",") != "u-peer,u-self" || len(f.registeredKeys) != 2 || len(f.registeredWrapped) != 2 || f.sent.ToType != 2 {
		t.Fatal("incomplete group registration")
	}
}

func TestGroupMissingMembershipNeverDowngrades(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.chats = &line.GetChatsResponse{}
	if _, err := c.Send("c-group", "hello"); err == nil || f.sends != 0 || f.registrations != 0 {
		t.Fatal("registered or sent with incomplete membership")
	}
}

func TestHistoryMissingGroupKeyNeverRegisters(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.history = []*line.Message{{From: "u-peer", To: "c-group", ToType: 2, Chunks: encryptedChunks(22, 33)}}
	result, err := c.History("c-group", 1)
	if err != nil || result[0].Status != "decryption_failed" || f.registrations != 0 {
		t.Fatal("history mutated group keys")
	}
}

func TestValidateTextUTF16AndEmpty(t *testing.T) {
	for _, text := range []string{"", " \n", string([]byte{0xff}), strings.Repeat("😀", 5001)} {
		if ValidateText(text) == nil {
			t.Fatal("invalid text accepted")
		}
	}
	if err := ValidateText(strings.Repeat("😀", 5000)); err != nil {
		t.Fatal(err)
	}
}

func TestMissingOrUnrestorableKeysNeverDowngrade(t *testing.T) {
	for _, missing := range []bool{false, true} {
		c, f, crypto, s := setup(t, false)
		if missing {
			s.state.ExportedKeys = nil
		} else {
			crypto.loadErr = errors.New("bad export")
		}
		broken, err := newClient(c.Session, func() (Crypto, error) { return crypto, nil })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := broken.Send("u-peer", "hello"); err == nil || f.sends != 0 {
			t.Fatal("sent without restored keys")
		}
	}
}

func TestGroupRegistrationAddsOmittedCallerAndExcludesInvitees(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
	f.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{
		MemberMids: line.FlexibleMidMap{"u-peer": true}, InviteeMids: line.FlexibleMidMap{"u-invited": true},
	}}}}}
	if _, err := c.Send("c-group", "hello"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.registeredMembers, ",") != "u-peer,u-self" {
		t.Fatal("caller omitted or invitee included")
	}
}
