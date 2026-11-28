package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// streamAPI replays synthetic operations once, then waits for cancellation.
type streamAPI struct {
	session.API
	frames       []string
	sent         bool
	contactCalls int
	chatCalls    int
	buddyCalls   int
	roomCalls    int
	buddies      map[string]line.BuddyProfile
	rooms        map[string]line.Room
	namesFail    bool
}

func (f *streamAPI) GetLastOpRevisionContext(context.Context) (int64, error) { return 1, nil }

func (f *streamAPI) ListenSSE(ctx context.Context, _ int64, emit func(string, string)) error {
	if !f.sent {
		f.sent = true
		for _, frame := range f.frames {
			emit("operation", frame)
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *streamAPI) GetContactsV2Context(_ context.Context, ids []string) (*line.ContactsResponse, error) {
	f.contactCalls++
	if f.namesFail {
		return nil, errors.New("synthetic lookup failure")
	}
	r := &line.ContactsResponse{Contacts: map[string]line.ContactWrapper{}}
	for _, id := range ids {
		r.Contacts[id] = line.ContactWrapper{Contact: line.Contact{DisplayName: map[string]string{"u-alice": "Alice", "u-bob": "Bob\x1b[2J"}[id]}}
	}
	return r, nil
}

func (f *streamAPI) GetChatsContext(_ context.Context, ids []string, _, _ bool) (*line.GetChatsResponse, error) {
	f.chatCalls++
	if f.namesFail {
		return nil, errors.New("synthetic lookup failure")
	}
	return &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: ids[0], ChatName: "Family"}}}, nil
}

func (f *streamAPI) GetBuddyProfileContext(_ context.Context, mid string) (*line.BuddyProfile, error) {
	f.buddyCalls++
	if f.namesFail {
		return nil, errors.New("synthetic lookup failure")
	}
	if b, ok := f.buddies[mid]; ok {
		return &b, nil
	}
	return nil, line.ErrBuddyNotFound
}

func (f *streamAPI) GetRoomsV2Context(_ context.Context, mids []string) ([]line.Room, error) {
	f.roomCalls++
	if f.namesFail {
		return nil, errors.New("synthetic lookup failure")
	}
	out := make([]line.Room, 0, len(mids))
	for _, mid := range mids {
		if r, ok := f.rooms[mid]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func operation(rev, opType int, msg *line.Message) string {
	data, _ := json.Marshal(line.Operation{Revision: json.Number(fmt.Sprint(rev)), Type: opType, Message: msg})
	return string(data)
}

func watchFrames() []string {
	created := json.Number(fmt.Sprint(time.Now().UnixMilli()))
	return []string{
		operation(2, 26, &line.Message{ID: "1", From: "u-alice", To: "u-self", CreatedTime: created, Text: "hello\nthere"}),
		operation(3, 40, nil), // read receipt from another session: hidden by default
		operation(4, 26, &line.Message{ID: "2", From: "u-bob", To: "c-group", ToType: 2, CreatedTime: created, Text: "hi all"}),
		operation(5, 25, &line.Message{ID: "3", From: "u-self", To: "u-alice", CreatedTime: created, Text: "on my way"}),
		operation(6, 26, &line.Message{ID: "4", From: "u-alice", To: "u-self", CreatedTime: created, ContentType: 14, ContentMetadata: map[string]string{"FILE_NAME": "notes.txt"}}),
	}
}

func watchApp(t *testing.T, terminal bool, f *streamAPI) (*App, *strings.Builder, *testStore) {
	t.Helper()
	a, _, _ := testApp(nil)
	store := &testStore{state: &session.State{Version: 1, MID: "u-self", AccessToken: "synthetic", Generation: "g", NoE2EE: true}}
	a.Manager.Store = store
	a.Manager.NewClient = func(string) session.API { return f }
	a.WatchLock = func() (func(), error) { return func() {}, nil }
	a.StdoutIsTerminal = terminal
	out := new(strings.Builder)
	a.Out = out
	return a, out, store
}

func TestWatchPipedAndJSONStayNDJSON(t *testing.T) {
	for _, tc := range []struct {
		terminal bool
		args     []string
	}{{false, []string{"watch", "--timeout", "200ms"}}, {true, []string{"watch", "--json", "--timeout", "200ms"}}, {false, []string{"watch", "--verbose", "--timeout", "200ms"}}} {
		f := &streamAPI{frames: watchFrames()}
		a, out, store := watchApp(t, tc.terminal, f)
		if err := a.Run(tc.args); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 5 || f.contactCalls+f.chatCalls != 0 || *store.state.WatchRevision != 6 {
			t.Fatalf("%v: %d lines, lookups %d, revision %d\n%s", tc.args, len(lines), f.contactCalls+f.chatCalls, *store.state.WatchRevision, out.String())
		}
		for _, l := range lines {
			var event map[string]any
			if err := json.Unmarshal([]byte(l), &event); err != nil {
				t.Fatalf("%v: not NDJSON: %q", tc.args, l)
			}
		}
		if !strings.Contains(lines[1], `"event":"operation"`) || !strings.Contains(lines[1], `"type":40`) {
			t.Fatalf("operation event changed: %q", lines[1])
		}
	}
}

func TestWatchReadableInTerminal(t *testing.T) {
	f := &streamAPI{frames: watchFrames()}
	a, out, store := watchApp(t, true, f)
	if err := a.Run([]string{"watch", "--timeout", "200ms"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	clock := time.Now().Format("15:04")
	for _, want := range []string{
		clock + "  Alice: hello ↵ there\n",
		clock + "  Family · Bob [2J: hi all\n",
		clock + "  You → Alice: on my way\n",
		clock + "  Alice: [File: notes.txt]\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "Event type") || strings.Contains(got, "{") || strings.Contains(got, "\x1b") || strings.Count(got, "\n") != 4 {
		t.Fatalf("unexpected readable output\n%q", got)
	}
	if f.contactCalls != 2 || f.chatCalls != 1 || *store.state.WatchRevision != 6 {
		t.Fatalf("names not cached or checkpoint wrong: contacts=%d chats=%d revision=%d", f.contactCalls, f.chatCalls, *store.state.WatchRevision)
	}
}

func TestWatchReadableVerboseLimitAndLookupFailure(t *testing.T) {
	f := &streamAPI{frames: watchFrames(), namesFail: true}
	a, out, store := watchApp(t, true, f)
	if err := a.Run([]string{"watch", "--verbose", "--limit", "3"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "u-alice: hello") || !strings.Contains(got, "—  Event type 40 (revision 3)") || !strings.Contains(got, "c-group · u-bob: hi all") || strings.Count(got, "\n") != 3 || *store.state.WatchRevision != 4 {
		t.Fatalf("verbose/limit output (revision %d)\n%s", *store.state.WatchRevision, got)
	}
	// Hidden events advance the checkpoint but don't count toward --limit.
	f = &streamAPI{frames: watchFrames()}
	a, out, store = watchApp(t, true, f)
	if err := a.Run([]string{"watch", "--limit", "2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 2 || *store.state.WatchRevision != 4 {
		t.Fatalf("hidden event counted: revision %d\n%s", *store.state.WatchRevision, out.String())
	}
}

func TestWatchReadablePostNotifications(t *testing.T) {
	created := json.Number(fmt.Sprint(time.Now().UnixMilli()))
	frames := []string{
		operation(2, 26, &line.Message{
			ID: "5001", From: "u-alice", To: "c-group", ToType: 2, CreatedTime: created, ContentType: 16,
			ContentMetadata: map[string]string{
				"serviceType": "GB",
				"text":        "Packing checklist\nTent, stove, headlamps",
				"postEndUrl":  "https://line.me/R/group/home/posts/post?homeId=c-group&postId=119988",
			},
		}),
		operation(3, 26, &line.Message{
			ID: "5002", From: "u-bob", To: "c-group", ToType: 2, CreatedTime: created, ContentType: 16,
			ContentMetadata: map[string]string{
				"serviceType":   "AB",
				"albumName":     "Summit photos",
				"postEndUrl":    "line://group/home/albums/album?albumId=123&albumIdV2=abc-v2",
				"chatId":        "c-group",
				"previewMedias": `"malformed"`,
			},
		}),
		operation(4, 26, &line.Message{
			ID: "5003", From: "u-alice", To: "c-group", ToType: 2, CreatedTime: created, ContentType: 0,
			Text: "Alice created a note fallback.",
			ContentMetadata: map[string]string{
				"ORGCONTP":    "POSTNOTIFICATION",
				"serviceType": "GB",
				"text":        "Shared note",
			},
		}),
	}
	f := &streamAPI{frames: frames}
	a, out, _ := watchApp(t, true, f)
	if err := a.Run([]string{"watch", "--timeout", "200ms"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Family · Alice: [Note: Packing checklist] ↵ Tent, stove, headlamps ↵ https://line.me/R/group/home/posts/post?homeId=c-group&postId=119988\n",
		"Family · Bob [2J: [Album: Summit photos] ↵ line://group/home/albums/album?albumId=123&albumIdV2=abc-v2\n",
		"Family · Alice: [Note: Shared note]\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "fallback") || strings.Contains(got, "[Malformed message content]") {
		t.Fatalf("watch leaked fallback or hid retained album fields:\n%s", got)
	}

	// Watch --json preserves legacy NDJSON projection
	fJSON := &streamAPI{frames: frames}
	aJSON, outJSON, _ := watchApp(t, true, fJSON)
	if err := aJSON.Run([]string{"watch", "--json", "--timeout", "200ms"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outJSON.String(), `"text":"Alice created a note fallback."`) {
		t.Fatalf("watch --json altered legacy wrapper text:\n%s", outJSON.String())
	}
}

func TestWatchReadableResolvesBuddyAndRoomNames(t *testing.T) {
	created := json.Number(fmt.Sprint(time.Now().UnixMilli()))
	f := &streamAPI{
		frames: []string{
			operation(2, 26, &line.Message{ID: "1", From: "u-official", To: "u-self", CreatedTime: created, Text: "deal alert"}),
			operation(3, 26, &line.Message{ID: "2", From: "u-alice", To: "r-room1", ToType: 1, CreatedTime: created, Text: "room hi"}),
		},
		buddies: map[string]line.BuddyProfile{
			"u-official": {Mid: "u-official", DisplayName: "LINE Store"},
		},
		rooms: map[string]line.Room{
			"r-room1": {Mid: "r-room1", MemberMids: line.FlexibleMidMap{"u-self": true, "u-alice": true}},
		},
	}
	a, out, _ := watchApp(t, true, f)
	if err := a.Run([]string{"watch", "--timeout", "200ms"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "LINE Store: deal alert") || !strings.Contains(got, "Alice · Alice: room hi") {
		t.Fatalf("buddy or room name missing in readable watch:\n%s", got)
	}
}
