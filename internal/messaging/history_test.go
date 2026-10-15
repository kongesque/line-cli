package messaging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type historyAPI struct {
	*fakeAPI
	pages   [][]*line.Message
	anchors []line.MessageIDWrapper
	counts  []int
	before  func(context.Context) error
}

func (f *historyAPI) GetPreviousMessagesV2Context(ctx context.Context, _ string, anchor line.MessageIDWrapper, count int) ([]*line.Message, error) {
	f.anchors = append(f.anchors, anchor)
	f.counts = append(f.counts, count)
	if f.before != nil {
		if err := f.before(ctx); err != nil {
			return nil, err
		}
	}
	if len(f.pages) == 0 {
		return nil, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}
func historyMessage(id string) *line.Message {
	return &line.Message{ID: id, From: "u-peer", To: "u-self", CreatedTime: "1", DeliveredTime: "9007199254740995", Text: "synthetic"}
}
func historySetup(t *testing.T) (*Client, *historyAPI) {
	t.Helper()
	c, f, _, _ := setup(t, true)
	api := &historyAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return api }
	return c, api
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *fault.Error
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
}
func TestHistoryPageCursorAndInclusiveAnchor(t *testing.T) {
	c, api := historySetup(t)
	first := historyMessage("9007199254740997")
	older := historyMessage("9007199254740993")
	api.history = []*line.Message{first}
	page, err := c.HistoryPageContext(context.Background(), "u-peer", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Exhausted || page.StopReason != "count_limit" || page.Order != "newest_first" {
		t.Fatalf("page: %+v", page)
	}
	cursor, err := parseHistoryCursor(page.NextCursor)
	if err != nil || cursor.MessageID != first.ID || cursor.DeliveredTime != first.DeliveredTime.String() {
		t.Fatalf("cursor: %+v %v", cursor, err)
	}
	api.pages = [][]*line.Message{{first, older}, {older}}
	next, err := c.HistoryPageContext(context.Background(), "u-peer", 1, page.NextCursor)
	if err != nil || len(next.Messages) != 1 || next.Messages[0].ID != older.ID || api.counts[0] != 2 || api.anchors[0].DeliveredTime != first.DeliveredTime {
		t.Fatalf("next: %+v %v", next, err)
	}
	end, err := c.HistoryPageContext(context.Background(), "u-peer", 1, next.NextCursor)
	if err != nil || !end.Exhausted || end.NextCursor != "" || end.StopReason != "end_of_available_history" || len(end.Messages) != 0 {
		t.Fatalf("end: %+v %v", end, err)
	}
}
func TestHistoryPageExclusiveAnchorDoesNotSkipTrimmedMessage(t *testing.T) {
	c, api := historySetup(t)
	token, _ := cursorFor("u-peer", historyMessage("10"))
	api.pages = [][]*line.Message{{historyMessage("9"), historyMessage("8")}, {historyMessage("8")}}
	page, err := c.HistoryPageContext(context.Background(), "u-peer", 1, token)
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ := parseHistoryCursor(page.NextCursor)
	if cursor.MessageID != "9" {
		t.Fatalf("skipped unreturned message: %+v", cursor)
	}
	next, err := c.HistoryPageContext(context.Background(), "u-peer", 1, page.NextCursor)
	if err != nil || len(next.Messages) != 1 || next.Messages[0].ID != "8" {
		t.Fatalf("next: %+v %v", next, err)
	}
}
func TestHistoryCursorValidationIsLocal(t *testing.T) {
	c, api := historySetup(t)
	c.Session.NewClient = func(string) session.API { t.Fatal("network for invalid cursor"); return api }
	valid, _ := cursorFor("u-peer", historyMessage("9007199254740993"))
	for _, token := range []string{"bad", strings.Repeat("a", 1025), base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"chat_id":"u-peer","message_id":"123","delivered_time":"2"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"chat_id":"u-peer","message_id":9007199254740993,"delivered_time":"2"}`))} {
		_, err := c.HistoryPageContext(context.Background(), "u-peer", 20, token)
		requireCode(t, err, "invalid_cursor")
	}
	_, err := c.HistoryPageContext(context.Background(), "u-other", 20, valid)
	requireCode(t, err, "invalid_cursor")
}
func TestHistoryPageRejectsInvalidOrNonadvancingData(t *testing.T) {
	for _, kind := range []string{"newer", "wrong chat", "missing delivered", "bad id", "nil", "anchor timestamp changed"} {
		t.Run(kind, func(t *testing.T) {
			c, api := historySetup(t)
			token, _ := cursorFor("u-peer", historyMessage("10"))
			msg := historyMessage("9")
			switch kind {
			case "newer":
				msg.ID = "11"
			case "wrong chat":
				msg.From = "u-other"
			case "missing delivered":
				msg.DeliveredTime = ""
			case "bad id":
				msg.ID = "bad"
			case "nil":
				msg = nil
			case "anchor timestamp changed":
				msg.ID = "10"
				msg.DeliveredTime = "5"
			}
			api.pages = [][]*line.Message{{msg}}
			_, err := c.HistoryPageContext(context.Background(), "u-peer", 20, token)
			requireCode(t, err, "history_invalid")
		})
	}
}
func TestHistoryPageShortPageOrderingAndEmptyEnd(t *testing.T) {
	c, api := historySetup(t)
	api.history = []*line.Message{historyMessage("8"), historyMessage("10"), historyMessage("8")}
	page, err := c.HistoryPageContext(context.Background(), "u-peer", 20, "")
	if err != nil || page.Exhausted || page.StopReason != "page_limit" || len(page.Messages) != 2 || page.Messages[0].ID != "10" {
		t.Fatalf("page: %+v %v", page, err)
	}
	end, err := c.HistoryPageContext(context.Background(), "u-peer", 20, page.NextCursor)
	if err != nil || !end.Exhausted {
		t.Fatalf("end: %+v %v", end, err)
	}
	// Legacy output still preserves server order and has no page fields.
	legacy, err := c.HistoryContext(context.Background(), "u-peer", 20)
	if err != nil || legacy[0].ID != "8" || len(legacy) != 3 {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	encoded, _ := json.Marshal(legacy)
	if strings.Contains(string(encoded), "delivered") || strings.Contains(string(encoded), "cursor") {
		t.Fatal("legacy contract changed")
	}
}
func TestLookupOlderTargetAndBudgets(t *testing.T) {
	c, api := historySetup(t)
	api.history = []*line.Message{historyMessage("100")}
	api.pages = [][]*line.Message{{historyMessage("100"), historyMessage("50")}, {historyMessage("50"), historyMessage("5")}}
	msg, err := c.findMessage(context.Background(), "u-peer", "5")
	if err != nil || msg.ID != "5" || len(api.anchors) != 2 {
		t.Fatalf("lookup: %+v %v", msg, err)
	}
	api.anchors = nil
	api.history = []*line.Message{historyMessage("100")}
	for i := 99; i > 80; i-- {
		api.pages = append(api.pages, []*line.Message{historyMessage(fmt.Sprint(i))})
	}
	_, err = c.findMessage(context.Background(), "u-peer", "1")
	requireCode(t, err, "lookup_incomplete")
	if len(api.anchors) != LookupMaxPages-1 {
		t.Fatal("lookup exceeded page budget")
	}
	api.pages = nil
	_, err = c.findMessage(context.Background(), "u-peer", "1")
	requireCode(t, err, "message_not_found")
	if !strings.Contains(err.Error(), "available history") {
		t.Fatal("claimed global absence")
	}
}
func TestLookupDeadlineAndCancellation(t *testing.T) {
	c, api := historySetup(t)
	api.history = []*line.Message{historyMessage("100")}
	api.before = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.findMessage(ctx, "u-peer", "1")
	if !errors.Is(err, context.DeadlineExceeded) || len(api.anchors) != 1 {
		t.Fatalf("deadline: %v", err)
	}
	api.anchors = nil
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = c.HistoryPageContext(ctx, "u-peer", 10, "")
	if !errors.Is(err, context.Canceled) || len(api.anchors) != 0 {
		t.Fatalf("cancel: %v", err)
	}
}
func TestRepliesRejectWrongChatBeforeAnyMutation(t *testing.T) {
	for _, chat := range []string{"u-peer", "c-group", "r-room"} {
		for _, file := range []bool{false, true} {
			c, api, _, store := setup(t, false)
			api.history = []*line.Message{{ID: "123", From: "u-peer", To: "c-other", ToType: 2}}
			var err error
			if file {
				_, err = c.SendFile(chat, Attachment{Name: "test.txt"}, "123")
			} else {
				_, err = c.SendReply(chat, "hello", "123")
			}
			requireCode(t, err, "history_invalid")
			if api.sends != 0 || api.registrations != 0 || store.state.LastReqSeq != 0 {
				t.Fatal("mutation before reply identity validation")
			}
			detail := mutationDetails(t, err)
			if detail.Mutation.Attempted || detail.Stage != "lookup" {
				t.Fatalf("outcome: %+v", detail)
			}
		}
	}
}
func TestMessageChatIdentity(t *testing.T) {
	c, _, _, _ := setup(t, true)
	for _, tc := range []struct {
		chat, from, to string
		kind           int
		valid          bool
	}{
		{"u-peer", "u-peer", "u-self", 0, true}, {"u-peer", "u-self", "u-peer", 0, true},
		{"u-peer", "u-other", "u-peer", 0, false}, {"u-peer", "u-peer", "u-other", 0, false},
		{"c-group", "u-peer", "c-group", 2, true}, {"r-room", "u-self", "r-room", 1, true},
		{"c-group", "u-peer", "c-group", 0, false}, {"c-group", "u-peer", "c-other", 2, false},
	} {
		if c.messageInChat(tc.chat, &line.Message{From: tc.from, To: tc.to, ToType: tc.kind}) != tc.valid {
			t.Fatalf("identity %+v", tc)
		}
	}
}

func (f *historyAPI) ReactContext(_ context.Context, seq int64, _ string, _ line.ReactionType) error {
	if seq != f.store.state.LastReqSeq {
		panic("mutation before persistent sequence")
	}
	f.sends++
	return nil
}
func (f *historyAPI) UnsendMessageContext(ctx context.Context, seq int64, id string) error {
	return f.ReactContext(ctx, seq, id, line.ReactionType{})
}
func (f *historyAPI) DownloadOBSWithSIDOptions(context.Context, string, string, string, line.OBSDownloadOptions) ([]byte, error) {
	return []byte("media"), nil
}
func (f *historyAPI) RefreshAccessToken(string) (*line.TokenV3IssueResult, error) {
	return &line.TokenV3IssueResult{AccessToken: "refreshed", RefreshToken: "rotated"}, nil
}

func TestAllActionsFindTargetsBeyondLatest100(t *testing.T) {
	for _, action := range []string{"reply", "react", "unsend", "download"} {
		t.Run(action, func(t *testing.T) {
			c, api := historySetup(t)
			for id := 201; id >= 102; id-- {
				api.history = append(api.history, historyMessage(fmt.Sprint(id)))
			}
			target := historyMessage("101")
			target.From, target.To = "u-self", "u-peer"
			target.ContentType = 14
			api.pages = [][]*line.Message{{api.history[99], target}}
			var err error
			switch action {
			case "reply":
				_, err = c.SendReply("u-peer", "hello", "101")
			case "react":
				_, err = c.React("u-peer", "101", "like", false)
			case "unsend":
				_, err = c.Unsend("u-peer", "101")
			case "download":
				var data []byte
				data, err = c.DownloadFile(context.Background(), "u-peer", "101")
				if string(data) != "media" {
					t.Fatal("download missed target")
				}
			}
			if err != nil || len(api.anchors) != 1 || api.registrations != 0 {
				t.Fatalf("action: %v", err)
			}
			if action != "download" && api.sends != 1 {
				t.Fatal("mutation count")
			}
		})
	}
}
func TestOlderLookupPreservesOwnershipAndReadOnlyBehavior(t *testing.T) {
	c, api := historySetup(t)
	api.history = []*line.Message{historyMessage("100")}
	api.pages = [][]*line.Message{{historyMessage("99")}}
	_, err := c.Unsend("u-peer", "99")
	if err == nil || !strings.Contains(err.Error(), "only your own") || api.sends != 0 || api.store.state.LastReqSeq != 0 || api.registrations != 0 {
		t.Fatalf("ownership: %v", err)
	}
}
func TestOlderHistoryReadRecoversAuthentication(t *testing.T) {
	c, api := historySetup(t)
	api.store.state.RefreshToken = "refresh"
	token, _ := cursorFor("u-peer", historyMessage("100"))
	api.pages = [][]*line.Message{{historyMessage("99")}}
	calls := 0
	api.before = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New(`{"code":119}`)
		}
		if api.store.state.AccessToken != "refreshed" {
			t.Fatal("read replayed before token persisted")
		}
		return nil
	}
	page, err := c.HistoryPageContext(context.Background(), "u-peer", 20, token)
	if err != nil || calls != 2 || len(page.Messages) != 1 || api.anchors[0] != api.anchors[1] || api.registrations != 0 || api.store.state.LastReqSeq != 0 {
		t.Fatalf("recovery: %+v %v", page, err)
	}
}

func TestPagedGroupReadsNeverRegisterKeys(t *testing.T) {
	c, f, _, store := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.history = []*line.Message{{ID: "123", From: "u-peer", To: "c-group", ToType: 2, DeliveredTime: "100", Chunks: encryptedChunks(22, 33)}}
	page, err := c.HistoryPageContext(context.Background(), "c-group", 1, "")
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Status != "decryption_failed" || f.registrations != 0 || store.state.LastReqSeq != 0 {
		t.Fatalf("read had side effects: %+v %v", page, err)
	}
}
