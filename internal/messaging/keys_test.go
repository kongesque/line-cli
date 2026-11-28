package messaging

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
)

func TestGroupKeyRotationGuardRetiredVsStaleOwnKey(t *testing.T) {
	t.Run("retired own key triggers fresh registration", func(t *testing.T) {
		c, f, crypto, _ := setup(t, false)
		// MyKeyIDs() returns 11; group key references retired own key 9 (< 11).
		f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 9, EncryptedSharedKey: "wrapped"}
		f.groupPublicKeys = map[string]line.E2EEPeerPublicKey{
			"u-self": {KeyID: 11, KeyData: testPublicKey},
			"u-peer": {KeyID: 22, KeyData: testPublicKey},
		}
		unwrapCalls := 0
		c.crypto = &unwrapHookCrypto{
			fakeCrypto: crypto,
			onUnwrap: func() error {
				unwrapCalls++
				if unwrapCalls == 1 {
					return &e2ee.MissingOwnKeyError{KeyID: 9}
				}
				return nil
			},
		}
		result, err := c.Send("c-group", "hello")
		if err != nil || result == nil || !result.GroupKeyRegistered || f.registrations != 1 || f.sends != 1 {
			t.Fatalf("retired own key should register fresh key: result=%+v err=%v regs=%d sends=%d", result, err, f.registrations, f.sends)
		}
	})

	for _, keyID := range []int{11, 15} {
		t.Run("current or newer own key blocks destructive rotation", func(t *testing.T) {
			c, f, crypto, store := setup(t, false)
			f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: keyID, EncryptedSharedKey: "wrapped"}
			f.groupPublicKeys = map[string]line.E2EEPeerPublicKey{
				"u-self": {KeyID: 11, KeyData: testPublicKey},
				"u-peer": {KeyID: 22, KeyData: testPublicKey},
			}
			crypto.unwrapErr = &e2ee.MissingOwnKeyError{KeyID: keyID}
			_, err := c.Send("c-group", "hello")
			var safe *fault.Error
			if !errors.As(err, &safe) || safe.Code != "invalid_session" || !strings.Contains(err.Error(), "line login") {
				t.Fatalf("expected invalid_session error for keyID=%d, got %v", keyID, err)
			}
			if f.registrations != 0 || f.sends != 0 || store.state.LastReqSeq != 0 {
				t.Fatalf("stale session mutated group or reserved sequence: regs=%d sends=%d seq=%d", f.registrations, f.sends, store.state.LastReqSeq)
			}
		})
	}
}

type unwrapHookCrypto struct {
	*fakeCrypto
	onUnwrap func() error
}

func (u *unwrapHookCrypto) UnwrapGroupSharedKey(chat string, key *line.E2EEGroupSharedKey) (int, error) {
	if err := u.onUnwrap(); err != nil {
		return 0, err
	}
	return u.fakeCrypto.UnwrapGroupSharedKey(chat, key)
}

func TestGroupKeyRegistrationValidatesAuthoritativeKeysAndMismatch(t *testing.T) {
	cases := []struct {
		name string
		keys map[string]line.E2EEPeerPublicKey
	}{
		{"empty map", map[string]line.E2EEPeerPublicKey{}},
		{"missing caller", map[string]line.E2EEPeerPublicKey{"u-peer": {KeyID: 22, KeyData: testPublicKey}}},
		{"stale caller key", map[string]line.E2EEPeerPublicKey{"u-self": {KeyID: 99, KeyData: testPublicKey}, "u-peer": {KeyID: 22, KeyData: testPublicKey}}},
		{"non-user member mid", map[string]line.E2EEPeerPublicKey{"u-self": {KeyID: 11, KeyData: testPublicKey}, "c-other": {KeyID: 22, KeyData: testPublicKey}}},
		{"zero member key id", map[string]line.E2EEPeerPublicKey{"u-self": {KeyID: 11, KeyData: testPublicKey}, "u-peer": {KeyID: 0, KeyData: testPublicKey}}},
		{"invalid member pubkey", map[string]line.E2EEPeerPublicKey{"u-self": {KeyID: 11, KeyData: testPublicKey}, "u-peer": {KeyID: 22, KeyData: "not-base64!"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f, _, store := setup(t, false)
			f.groupErr = line.ErrGroupKeyNotFound
			f.groupPublicKeys = tc.keys
			_, err := c.SendMentionsWithOptionsContext(context.Background(), "c-group", "hello", "", nil, SendOptions{AllowPlaintext: true})
			if err == nil || f.registrations != 0 || f.sends != 0 || store.state.LastReqSeq != 0 {
				t.Fatalf("case %s should fail closed: err=%v regs=%d sends=%d", tc.name, err, f.registrations, f.sends)
			}
		})
	}

	t.Run("TalkException 99 member mismatch fails clearly without plaintext downgrade", func(t *testing.T) {
		c, f, _, store := setup(t, false)
		f.groupErr = line.ErrGroupKeyNotFound
		f.groupPublicKeys = map[string]line.E2EEPeerPublicKey{
			"u-self": {KeyID: 11, KeyData: testPublicKey},
			"u-peer": {KeyID: 22, KeyData: testPublicKey},
		}
		wire := line.NewClient("synthetic-token")
		registrations := 0
		wire.HTTPClient.Transport = claimTransport(func(r *http.Request) (*http.Response, error) {
			registrations++
			return claimResponse(r, http.StatusBadRequest, `{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":99,"reason":"invalid member count"}}`), nil
		})
		api := &groupRegistrationAPI{fakeAPI: f, wire: wire}
		c.Session.NewClient = func(string) session.API { return api }
		_, err := c.SendMentionsWithOptionsContext(context.Background(), "c-group", "hello", "", nil, SendOptions{AllowPlaintext: true})
		var safe *fault.Error
		if !errors.As(err, &safe) || safe.Code != "group_member_mismatch" {
			t.Fatalf("expected group_member_mismatch fault, got %v", err)
		}
		detail := mutationDetails(t, err)
		if registrations != 1 || f.sends != 0 || store.state.LastReqSeq != 0 || detail.Mutation.Steps[0].Outcome != fault.Rejected {
			t.Fatalf("mismatch retried, downgraded, or misclassified: regs=%d sends=%d steps=%+v", registrations, f.sends, detail.Mutation.Steps)
		}
	})
}

func TestSend1to1ReceiverKeyRejectedActionableWithoutRetry(t *testing.T) {
	c, f, _, store := setup(t, false)
	wire := line.NewClient("synthetic-token")
	sendCalls := 0
	wire.HTTPClient.Transport = claimTransport(func(r *http.Request) (*http.Response, error) {
		sendCalls++
		return claimResponse(r, http.StatusBadRequest, `{"code":10051,"message":"RESPONSE_ERROR","data":{"name":"TalkException","code":83,"reason":"invalid receiver key"}}`), nil
	})
	c.Session.NewClient = func(string) session.API { return &sendWireAPI{fakeAPI: f, wire: wire} }

	_, err := c.Send("u-peer", "hello")
	var safe *fault.Error
	if !errors.As(err, &safe) || safe.Code != "e2ee_receiver_key_rejected" {
		t.Fatalf("expected e2ee_receiver_key_rejected fault, got %v", err)
	}
	detail := mutationDetails(t, err)
	if sendCalls != 1 || detail.Mutation.Outcome != fault.Rejected || store.state.LastReqSeq == 0 {
		t.Fatalf("receiver-key rejection retried or misclassified: calls=%d outcome=%s", sendCalls, detail.Mutation.Outcome)
	}
}

type sendWireAPI struct {
	*fakeAPI
	wire *line.Client
}

func (a *sendWireAPI) SendMessageContext(ctx context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	return a.wire.SendMessageContext(ctx, seq, msg)
}

type flakyKeyAPI struct {
	*fakeAPI
	peerFailures  int
	groupFailures int
	peerCalls     int
	groupCalls    int
	transientErr  error
}

func (f *flakyKeyAPI) GetE2EEPublicKeyContext(_ context.Context, _ string, _ int, id int) (*line.E2EEPublicKey, error) {
	f.peerCalls++
	if f.peerCalls <= f.peerFailures {
		return nil, f.transientErr
	}
	return f.key, nil
}

func (f *flakyKeyAPI) GetE2EEGroupSharedKeyContext(_ context.Context, _ string, id int) (*line.E2EEGroupSharedKey, error) {
	f.groupCalls++
	if f.groupCalls <= f.groupFailures {
		return nil, f.transientErr
	}
	return f.group, nil
}

func TestReadKeyFetchTransientRetryCooldownAndCancellation(t *testing.T) {
	t.Run("transient peer fetch succeeds on retry with 2s/4s backoff", func(t *testing.T) {
		c, f, _, _ := setup(t, false)
		flaky := &flakyKeyAPI{fakeAPI: f, peerFailures: 2, transientErr: io.ErrUnexpectedEOF}
		c.Session.NewClient = func(string) session.API { return flaky }
		var delays []time.Duration
		c.wait = func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}
		if err := c.peerByID(context.Background(), "u-peer", 22); err != nil {
			t.Fatalf("expected retry success, got %v", err)
		}
		if flaky.peerCalls != 3 || !reflect.DeepEqual(delays, []time.Duration{2 * time.Second, 4 * time.Second}) {
			t.Fatalf("calls=%d delays=%v", flaky.peerCalls, delays)
		}
	})

	t.Run("exhausted retries engage per-client cooldown then recover after cooldown", func(t *testing.T) {
		c, f, _, _ := setup(t, false)
		now := time.Unix(1000, 0)
		c.Session.Now = func() time.Time { return now }
		f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, Creator: "u-self", CreatorKeyID: 11, ReceiverKeyID: 11, EncryptedSharedKey: "wrapped"}
		flaky := &flakyKeyAPI{fakeAPI: f, groupFailures: 100, transientErr: &line.ResponseError{Status: http.StatusServiceUnavailable}}
		c.Session.NewClient = func(string) session.API { return flaky }
		var delays []time.Duration
		c.wait = func(_ context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		}

		// First lookup tries initial + 3 retries (2s, 4s, 8s) = 4 attempts.
		if err := c.groupKey(context.Background(), "c-group", 33); err == nil {
			t.Fatal("expected failure")
		}
		if flaky.groupCalls != 4 || !reflect.DeepEqual(delays, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}) {
			t.Fatalf("first outage: calls=%d delays=%v", flaky.groupCalls, delays)
		}

		// Second lookup within cooldown fails fast in 1 attempt with no waits.
		delays = nil
		if err := c.groupKey(context.Background(), "c-group", 33); err == nil {
			t.Fatal("expected fast failure during cooldown")
		}
		if flaky.groupCalls != 5 || len(delays) != 0 {
			t.Fatalf("cooldown not enforced: calls=%d delays=%v", flaky.groupCalls, delays)
		}

		// After cooldown expires and service recovers, retries resume and succeed.
		now = now.Add(keyRetryCooldown + time.Second)
		flaky.groupFailures = 6 // fail call #6, succeed on retry call #7
		if err := c.groupKey(context.Background(), "c-group", 33); err != nil {
			t.Fatalf("expected recovery after cooldown, got %v", err)
		}
		if flaky.groupCalls != 7 || !reflect.DeepEqual(delays, []time.Duration{2 * time.Second}) {
			t.Fatalf("post-cooldown recovery: calls=%d delays=%v", flaky.groupCalls, delays)
		}
	})

	t.Run("non-transient and auth errors never retry", func(t *testing.T) {
		c, f, _, _ := setup(t, false)
		flaky := &flakyKeyAPI{fakeAPI: f, peerFailures: 10, transientErr: errors.New("permanent validation failure")}
		c.Session.NewClient = func(string) session.API { return flaky }
		c.wait = func(context.Context, time.Duration) error {
			t.Fatal("non-transient error must not sleep/retry")
			return nil
		}
		if err := c.peerByID(context.Background(), "u-peer", 22); err == nil || flaky.peerCalls != 1 {
			t.Fatalf("non-transient retried: calls=%d err=%v", flaky.peerCalls, err)
		}
	})

	t.Run("context cancellation aborts retry backoff immediately", func(t *testing.T) {
		c, f, _, _ := setup(t, false)
		flaky := &flakyKeyAPI{fakeAPI: f, peerFailures: 10, transientErr: io.ErrUnexpectedEOF}
		c.Session.NewClient = func(string) session.API { return flaky }
		ctx, cancel := context.WithCancel(context.Background())
		c.wait = func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}
		err := c.peerByID(ctx, "u-peer", 22)
		if !errors.Is(err, context.Canceled) || flaky.peerCalls != 1 {
			t.Fatalf("cancellation ignored: calls=%d err=%v", flaky.peerCalls, err)
		}
	})
}
