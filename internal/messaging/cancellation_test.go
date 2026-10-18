package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type cancellationAPI struct {
	*fakeAPI
	cancel context.CancelFunc
	phase  string
	calls  int
}

func (f *cancellationAPI) GetBlockedContactIdsContext(ctx context.Context) ([]string, error) {
	f.calls++
	if f.phase == "blocked" {
		f.cancel()
	}
	return nil, nil
}
func (f *cancellationAPI) GetRecentMessagesV2Context(ctx context.Context, chat string, limit int) ([]*line.Message, error) {
	f.calls++
	if f.phase == "lookup" {
		f.cancel()
	}
	return f.history, nil
}
func (f *cancellationAPI) SendMessageContext(ctx context.Context, seq int64, msg *line.Message) (*line.Message, error) {
	f.calls++
	f.cancel()
	if f.phase == "confirmed" {
		return f.fakeAPI.SendMessage(seq, msg)
	}
	f.sends++
	return nil, ctx.Err()
}
func (f *cancellationAPI) NegotiateE2EEPublicKeyContext(ctx context.Context, mid string) (*line.E2EEPublicKey, error) {
	f.calls++
	if f.phase == "negotiate" {
		f.cancel()
	}
	return f.key, nil
}

func TestAlreadyCancelledOperationsMakeNoRemoteCalls(t *testing.T) {
	c, _, _, store := setup(t, true)
	c.Session.NewClient = func(string) session.API { t.Fatal("created remote client after cancellation"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := []func() error{
		func() error { _, e := c.HistoryContext(ctx, "u-peer", 10); return e },
		func() error {
			_, e := c.SendMentionsWithOptionsContext(ctx, "u-peer", "hello", "", nil, SendOptions{AllowPlaintext: true})
			return e
		},
		func() error {
			_, e := c.SendFileContext(ctx, "c-group", Attachment{Name: "test.txt", Data: []byte("test")}, "")
			return e
		},
		func() error { _, e := c.DownloadFile(ctx, "u-peer", "123"); return e },
		func() error { _, e := c.ReactContext(ctx, "u-peer", "123", "like", false); return e },
		func() error { _, e := c.UnsendContext(ctx, "u-peer", "123"); return e },
	}
	for i, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if store.state.LastReqSeq != 0 {
		t.Fatal("reserved sequence after cancellation")
	}
}

func TestCancellationBetweenPreparationAndMutation(t *testing.T) {
	for _, phase := range []string{"blocked", "negotiate", "lookup"} {
		t.Run(phase, func(t *testing.T) {
			c, api, _, store := setup(t, phase != "negotiate")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			api.history = []*line.Message{{ID: "123", From: "u-self", To: "u-peer"}}
			f := &cancellationAPI{fakeAPI: api, cancel: cancel, phase: phase}
			c.Session.NewClient = func(string) session.API { return f }
			var err error
			if phase == "lookup" {
				_, err = c.UnsendContext(ctx, "u-peer", "123")
			} else {
				_, err = c.SendMentionsWithOptionsContext(ctx, "u-peer", "hello", "", nil, SendOptions{AllowPlaintext: true})
			}
			if !errors.Is(err, context.Canceled) || api.sends != 0 || store.state.LastReqSeq != 0 {
				t.Fatalf("err=%v sends=%d seq=%d", err, api.sends, store.state.LastReqSeq)
			}
		})
	}
}

func TestCancellationAfterDispatchPreservesOutcome(t *testing.T) {
	for _, phase := range []string{"unknown", "confirmed", "plain-file"} {
		t.Run(phase, func(t *testing.T) {
			c, api, _, _ := setup(t, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &cancellationAPI{fakeAPI: api, cancel: cancel, phase: phase}
			if phase == "plain-file" {
				f.phase = "confirmed"
			}
			c.Session.NewClient = func(string) session.API { return f }
			var result *SendResult
			var err error
			if phase == "plain-file" {
				result, err = c.SendFileWithOptionsContext(ctx, "u-peer", Attachment{Name: "test.txt"}, "", SendOptions{AllowPlaintext: true})
			} else {
				result, err = c.SendMentionsWithOptionsContext(ctx, "u-peer", "hello", "", nil, SendOptions{AllowPlaintext: true})
			}
			if api.sends != 1 {
				t.Fatalf("sends=%d", api.sends)
			}
			switch phase {
			case "confirmed":
				if err != nil || result == nil || result.ID != "server-id" {
					t.Fatalf("lost receipt: %v %v", result, err)
				}
			case "unknown":
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "delivery may have occurred") {
					t.Fatalf("lost uncertain outcome: %v", err)
				}
			case "plain-file":
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "message server-id was created") {
					t.Fatalf("lost created message: %v", err)
				}
			}
		})
	}
}

type cancelCrypto struct {
	Crypto
	cancel context.CancelFunc
}

func (c cancelCrypto) WrapGroupKeyForMember(pub string, id int) (string, error) {
	c.cancel()
	return c.Crypto.WrapGroupKeyForMember(pub, id)
}
func TestCancellationBeforeGroupKeyRegistration(t *testing.T) {
	c, api, crypto, _ := setup(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.groupErr = line.ErrGroupKeyNotFound
	api.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-peer": true}}}}}}
	c.crypto = cancelCrypto{Crypto: crypto, cancel: cancel}
	_, err := c.SendContext(ctx, "c-group", "hello")
	if !errors.Is(err, context.Canceled) || api.registrations != 0 || api.sends != 0 {
		t.Fatalf("err=%v registrations=%d sends=%d", err, api.registrations, api.sends)
	}
}

func (f *cancellationAPI) UploadOBSWithSIDContext(ctx context.Context, _ []byte, _ string) (string, error) {
	f.calls++
	f.cancel()
	return "", ctx.Err()
}
func TestCancelledEncryptedUploadRetainsUncertainOutcome(t *testing.T) {
	c, api, _, _ := setup(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &cancellationAPI{fakeAPI: api, cancel: cancel}
	c.Session.NewClient = func(string) session.API { return f }
	_, err := c.SendFileWithOptionsContext(ctx, "u-peer", Attachment{Name: "test.txt"}, "", SendOptions{AllowPlaintext: true})
	var mutation *MutationError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &mutation) || !strings.Contains(err.Error(), "uploaded file may remain") || api.sends != 0 {
		t.Fatalf("err=%v sends=%d", err, api.sends)
	}
}

func (f *cancellationAPI) RegisterE2EEGroupKeyContext(ctx context.Context, _ int, _ string, _ []string, _ []int, _ []string) error {
	f.registrations++
	f.cancel()
	return ctx.Err()
}

func TestCancelledGroupRegistrationRetainsUncertainOutcome(t *testing.T) {
	c, api, _, _ := setup(t, false)
	api.groupErr = line.ErrGroupKeyNotFound
	api.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-peer": true}}}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &cancellationAPI{fakeAPI: api, cancel: cancel}
	c.Session.NewClient = func(string) session.API { return f }
	_, err := c.SendContext(ctx, "c-group", "synthetic")
	var mutation *MutationError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &mutation) || !strings.Contains(err.Error(), "registration did not return success") || api.sends != 0 || f.registrations != 1 {
		t.Fatalf("err=%v sends=%d registrations=%d", err, api.sends, f.registrations)
	}
}
