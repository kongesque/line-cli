package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// TestLiveSecuritySend sends a synthetic text and file (or only the file when
// a previously confirmed text receipt is explicitly supplied). It requires explicit
// human authorization for the target, fixed content, upload, and possible group
// key registration. Never enable it in ordinary CI or retry after an uncertain
// mutation outcome. Request bodies and credentials remain in memory only.
func TestLiveSecuritySend(t *testing.T) {
	if os.Getenv("LINE_CLI_LIVE_SECURITY_SEND") != "1" {
		t.Skip("explicit live send authorization required")
	}
	chat := os.Getenv("LINE_CLI_LIVE_SECURITY_CHAT")
	if ValidateChatID(chat) != nil {
		t.Fatal("explicit valid test chat ID required")
	}
	const content = "Security test from LINE CLI"
	const filename = "line-security-test.txt"
	file := Attachment{Name: filename, Data: []byte(content + "\n")}
	unlock, err := session.Lock()
	if err != nil {
		t.Fatal("session lock unavailable")
	}
	defer unlock()
	manager := session.NewManager(session.KeychainStore{})
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal("saved session unavailable")
	}
	if state.NoE2EE || state.Invalidated || len(state.ExportedKeys) == 0 {
		t.Fatal("session is not ready for encrypted sends")
	}
	before := state.LastReqSeq
	t.Log("session: saved encryption keys present; plaintext mode disabled")
	sends, uploads, registrations, requests := 0, 0, 0, 0
	defer func() {
		t.Logf("dispatch counts: sends=%d uploads=%d group_registrations=%d HTTPS_requests=%d", sends, uploads, registrations, requests)
	}()
	fail := func(stage string, err error) {
		if d := line.RequestDiagnostics(err); d != nil {
			encoded, _ := json.Marshal(d)
			t.Logf("safe diagnostic: %s", encoded)
		}
		var mutation *MutationError
		if errors.As(err, &mutation) {
			t.Logf("mutation stage=%s outcome=%s steps=%+v", mutation.Stage, mutation.Mutation.Outcome, mutation.Mutation.Steps)
		}
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			t.Log("cause type:", fmt.Sprintf("%T", cause))
		}
		t.Fatalf("%s failed; do not retry automatically", stage)
	}
	factory := manager.NewClient
	manager.NewClient = func(token string) session.API {
		api := factory(token).(*line.Client)
		wrap := func(base http.RoundTripper) http.RoundTripper {
			if base == nil {
				base = http.DefaultTransport
			}
			return liveSecurityTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" {
					return nil, errors.New("live inspection rejected non-HTTPS request")
				}
				requests++
				isSend := strings.HasSuffix(r.URL.Path, "/sendMessage")
				isUpload := r.URL.Host == "obs.line-apps.com" && r.Method == http.MethodPost
				if isSend || isUpload {
					if r.GetBody == nil {
						return nil, errors.New("live inspection requires replayable local request body")
					}
					body, err := r.GetBody()
					if err != nil {
						return nil, errors.New("cannot inspect request")
					}
					raw, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
					body.Close()
					if err != nil || len(raw) > 1<<20 {
						return nil, errors.New("cannot inspect complete request")
					}
					defer clear(raw)
					if bytes.Contains(raw, []byte(content)) || bytes.Contains(raw, []byte(filename)) {
						return nil, errors.New("live inspection blocked plaintext content or filename")
					}
					if isSend {
						var args []json.RawMessage
						var message line.Message
						if json.Unmarshal(raw, &args) != nil || len(args) != 2 || json.Unmarshal(args[1], &message) != nil {
							return nil, errors.New("unexpected send envelope")
						}
						if message.To != chat || len(message.Chunks) != 5 || message.Text != "" || message.ContentMetadata["ENC_KM"] != "" || message.ContentMetadata["FILE_NAME"] != "" {
							return nil, errors.New("live inspection blocked unsealed envelope")
						}
						sends++
						if sends > 2 {
							return nil, errors.New("live inspection blocked additional send")
						}
					} else {
						if !strings.Contains(r.URL.Path, "/emf") || bytes.Equal(raw, file.Data) {
							return nil, errors.New("live inspection blocked plaintext attachment")
						}
						uploads++
						if uploads > 1 {
							return nil, errors.New("live inspection blocked additional upload")
						}
					}
				}
				if strings.HasSuffix(r.URL.Path, "/registerE2EEGroupKey") {
					registrations++
				}
				resp, err := base.RoundTrip(r)
				if isSend || isUpload {
					if resp != nil {
						t.Logf("transport send=%v upload=%v status=%d object_id_header=%v", isSend, isUpload, resp.StatusCode, resp.Header.Get("x-obs-oid") != "")
					}
					if err != nil {
						t.Logf("transport error type=%T", err)
					}
				}
				return resp, err
			})
		}
		api.HTTPClient.Transport = wrap(api.HTTPClient.Transport)
		api.OBSClient.Transport = wrap(api.OBSClient.Transport)
		return api
	}
	client, err := New(manager)
	if err != nil || client.crypto == nil || client.keyError != nil {
		t.Fatal("could not restore saved encryption keys")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var capability *line.MediaMessageFlowResponse
	err = manager.DoContext(ctx, func(api session.API) (err error) {
		capability, err = api.DetermineMediaMessageFlowContext(ctx, chat)
		return
	})
	if err != nil || capability == nil || capability.FlowMap["14"] != 2 {
		t.Fatal("encrypted file capability is unavailable; no test messages were sent")
	}
	textResult := &SendResult{ID: os.Getenv("LINE_CLI_LIVE_SECURITY_TEXT_ID"), Encrypted: true}
	expectedSends := 1
	if textResult.ID == "" {
		expectedSends = 2
		textResult, err = client.SendContext(ctx, chat, content)
		if err != nil {
			fail("text send", err)
		}
		if textResult == nil || !textResult.Encrypted {
			t.Fatal("text receipt was not encrypted")
		}
		t.Logf("text confirmed: id=%s encrypted=true", textResult.ID)
	} else {
		t.Log("read-only verification of previously confirmed text; text will not be resent")
	}
	fileResult, err := client.SendFileContext(ctx, chat, file, "")
	if err != nil {
		fail("file send", err)
	}
	if fileResult == nil || !fileResult.Encrypted {
		t.Fatal("file receipt was not encrypted")
	}
	t.Logf("file confirmed: id=%s encrypted=true", fileResult.ID)
	raw, err := client.recentMessages(ctx, chat, 20)
	if err != nil {
		t.Fatal("history verification failed; messages were sent; do not resend")
	}
	found := map[string]bool{}
	for _, m := range raw {
		if m == nil || (m.ID != textResult.ID && m.ID != fileResult.ID) {
			continue
		}
		if len(m.Chunks) != 5 || m.Text != "" || m.ContentMetadata["ENC_KM"] != "" || m.ContentMetadata["FILE_NAME"] != "" {
			t.Fatal("server history exposed unsealed content or attachment metadata")
		}
		found[m.ID] = true
	}
	if !found[textResult.ID] || !found[fileResult.ID] {
		t.Fatal("new messages absent from recent history; do not resend")
	}
	var decoded []Message
	for _, m := range raw {
		if m != nil && (m.ID == textResult.ID || m.ID == fileResult.ID) {
			decoded = append(decoded, client.DecodeContext(ctx, chat, m))
		}
	}
	verified := map[string]bool{}
	for _, m := range decoded {
		if m.ID == textResult.ID {
			verified[m.ID] = m.Encrypted && m.Status == "decrypted" && m.Text == content
		}
		if m.ID == fileResult.ID {
			verified[m.ID] = m.Encrypted && m.Content.Status == "ok" && m.Content.Attachment != nil && m.Content.Attachment.Name != nil && *m.Content.Attachment.Name == filename
		}
	}
	if !verified[textResult.ID] || !verified[fileResult.ID] {
		t.Fatal("decrypted history did not match test content; do not resend")
	}
	data, err := client.DownloadFile(ctx, chat, fileResult.ID)
	if err != nil || !bytes.Equal(data, file.Data) {
		t.Fatal("attachment authentication or byte comparison failed; do not resend")
	}
	clear(data)
	after, err := manager.Store.Load()
	if err != nil || after.LastReqSeq <= before {
		t.Fatal("request sequence persistence not verified")
	}
	if sends != expectedSends || uploads != 1 {
		t.Fatal("unexpected mutation dispatch count")
	}
	t.Logf("verified: sealed wire envelopes; encrypted upload; authenticated download matches; sends=%d uploads=%d group_registrations=%d HTTPS_requests=%d sequence_advanced=true", sends, uploads, registrations, requests)
}

type liveSecurityTransport func(*http.Request) (*http.Response, error)

func (f liveSecurityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestLiveSecurityMediaFlow reads only the selected chat's declared media policy.
func TestLiveSecurityMediaFlow(t *testing.T) {
	if os.Getenv("LINE_CLI_LIVE_SECURITY_READ") != "1" {
		t.Skip("explicit live read authorization required")
	}
	chat := os.Getenv("LINE_CLI_LIVE_SECURITY_CHAT")
	if ValidateChatID(chat) != nil {
		t.Fatal("explicit valid test chat required")
	}
	unlock, err := session.Lock()
	if err != nil {
		t.Fatal("session lock unavailable")
	}
	defer unlock()
	manager := session.NewManager(session.KeychainStore{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Match Chrome's read-only negotiation order before requesting media flow.
	// Inspect only capability fields; never print group keys or member identities.
	if toType(chat) != 0 {
		var key *line.E2EEGroupSharedKey
		err = manager.DoContext(ctx, func(api session.API) (err error) {
			key, err = api.GetLastE2EEGroupSharedKeyContext(ctx, chat)
			return
		})
		if err != nil || key == nil {
			t.Fatal("group capability read failed; no key registration attempted")
		}
		textAllowed, fileAllowed := false, false
		for _, kind := range key.AllowedTypes {
			textAllowed = textAllowed || kind == 0
			fileAllowed = fileAllowed || kind == 14
		}
		t.Logf("group negotiation: spec_version=%d allowed_types_present=%v text_allowed=%v file_allowed=%v", key.SpecVersion, key.AllowedTypes != nil, textAllowed, fileAllowed)
	}
	var flow *line.MediaMessageFlowResponse
	err = manager.DoContext(ctx, func(api session.API) (err error) { flow, err = api.DetermineMediaMessageFlowContext(ctx, chat); return })
	if err != nil || flow == nil {
		t.Fatal("media capability read failed")
	}
	t.Logf("declared media flows: image=%d file=%d", flow.FlowMap["1"], flow.FlowMap["14"])
	if flow.FlowMap["14"] != 1 {
		return
	}
	state, err := manager.Store.Load()
	if err != nil {
		t.Fatal("saved session unavailable")
	}
	before := state.LastReqSeq
	factory := manager.NewClient
	blocked := 0
	manager.NewClient = func(token string) session.API {
		api := factory(token).(*line.Client)
		guard := liveSecurityTransport(func(r *http.Request) (*http.Response, error) {
			// This phase is explicitly read-only. Only capability and blocked-contact
			// reads may reach the network, even if a regression attempts a mutation.
			if r.URL.Scheme != "https" || (!strings.HasSuffix(r.URL.Path, "/determineMediaMessageFlow") && !strings.HasSuffix(r.URL.Path, "/getBlockedContactIds")) {
				blocked++
				return nil, errors.New("read-only inspection blocked unexpected request")
			}
			return http.DefaultTransport.RoundTrip(r)
		})
		api.HTTPClient.Transport = guard
		api.OBSClient.Transport = guard
		return api
	}
	client, err := New(manager)
	if err != nil {
		t.Fatal("session initialization failed")
	}
	_, err = client.SendFileContext(ctx, chat, Attachment{Name: "line-security-test.txt", Data: []byte("Security test from LINE CLI\n")}, "")
	var refusal *fault.Error
	if !errors.As(err, &refusal) || refusal.Code != "encryption_required" || blocked != 0 {
		t.Fatal("default file policy did not refuse before any mutation attempt")
	}
	after, err := manager.Store.Load()
	if err != nil || after.LastReqSeq != before {
		t.Fatal("read-only refusal changed request sequence")
	}
	t.Log("verified live default file refusal: encryption_required; uploads=0 sends=0 registrations=0 sequence_unchanged=true")
}
