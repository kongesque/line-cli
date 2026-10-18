package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
)

const mentionPeer = "Uaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestOutgoingMentionsValidation(t *testing.T) {
	text := "😀 @Peer @All"
	good := []Mention{{Start: 3, End: 8, MID: mentionPeer}, {Start: 9, End: 13, All: true}}
	raw, err := EncodeMentions(text, good)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decodeMentions(raw, text)
	if !ok || len(got) != 2 || got[0] != good[0] || got[1] != good[1] {
		t.Fatal("mention roundtrip failed")
	}
	for _, bad := range [][]Mention{
		{{Start: 1, End: 2, MID: mentionPeer}}, {{Start: -1, End: 2, MID: mentionPeer}},
		{{Start: 3, End: 99, MID: mentionPeer}}, {{Start: 3, End: 3, MID: mentionPeer}},
		{{Start: 3, End: 8, MID: "u-short"}}, {{Start: 3, End: 8, MID: "C" + mentionPeer[1:]}},
		{{Start: 3, End: 8}}, {{Start: 3, End: 8, MID: mentionPeer, All: true}},
		{good[0], {Start: 7, End: 9, All: true}}, {good[0], good[0]},
		make([]Mention, 101),
	} {
		if _, err := EncodeMentions(text, bad); err == nil {
			t.Fatalf("accepted invalid mentions: %#v", bad)
		}
	}
	if _, err := EncodeMentions("\xff", good); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	for _, raw := range []string{"null", `{}`, `[null]`, `[{"end":2,"all":true}]`, `[{"start":0,"end":null,"all":true}]`, `[{"start":0,"end":2,"unknown":true}]`, `[] []`, strings.Repeat(" ", maxContentBytes+1)} {
		if _, err := ParseMentions(raw); err == nil {
			t.Fatalf("accepted invalid JSON %q", raw[:min(len(raw), 80)])
		}
	}
	if rows, err := ParseMentions(`[{"start":0,"end":4,"all":true}]`); err != nil || len(rows) != 1 || !rows[0].All {
		t.Fatal("valid mention-all rejected")
	}
}

func TestSendMentionsPreservesEncryptionReplyAndSequence(t *testing.T) {
	for _, plain := range []bool{true, false} {
		for _, chat := range []string{"u-peer", "c-group"} {
			c, f, crypto, store := setup(t, plain)
			f.group = &line.E2EEGroupSharedKey{GroupKeyID: 33, CreatorKeyID: 22, ReceiverKeyID: 11, Creator: "u-peer", EncryptedSharedKey: "wrapped"}
			f.history = []*line.Message{{ID: "9007199254740993", From: "u-self", To: chat, ToType: toType(chat)}}
			result, err := c.SendMentionsWithOptionsContext(context.Background(), chat, "😀 @Peer", "9007199254740993", []Mention{{Start: 3, End: 8, MID: mentionPeer}}, SendOptions{AllowPlaintext: true})
			if err != nil {
				t.Fatal(err)
			}
			if result.Encrypted == plain || f.sends != 1 || f.sequence != store.state.LastReqSeq || f.sent.RelatedMessageID != "9007199254740993" || f.registrations != 0 {
				t.Fatal("send contract changed")
			}
			if plain && f.sent.Text != "😀 @Peer" || !plain && (f.sent.Text != "" || len(f.sent.Chunks) != 5) {
				t.Fatal("incorrect wire text/encryption")
			}
			// Incoming decoder reads outer metadata against authenticated text.
			crypto.payload = `{"text":"😀 @Peer"}`
			decoded := c.Decode(chat, f.sent)
			if decoded.Content.Status != "ok" || len(decoded.Content.Mentions) != 1 || decoded.Content.Mentions[0].MID != mentionPeer {
				t.Fatalf("mention decode: %#v", decoded.Content)
			}
		}
	}
}

func TestSendMentionsInvalidOrCanceledNeverMutates(t *testing.T) {
	for _, tc := range []struct {
		chat     string
		mentions []Mention
		canceled bool
	}{
		{"c-group", []Mention{{Start: 1, End: 2, MID: mentionPeer}}, false},
		{"u-peer", []Mention{{Start: 3, End: 8, All: true}}, false},
		{"c-group", []Mention{{Start: 3, End: 8, MID: mentionPeer}}, true},
	} {
		c, f, _, store := setup(t, false)
		ctx, cancel := context.WithCancel(context.Background())
		if tc.canceled {
			cancel()
		}
		defer cancel()
		before := store.state.LastReqSeq
		if _, err := c.SendMentionsContext(ctx, tc.chat, "😀 @Peer", "", tc.mentions); err == nil {
			t.Fatal("accepted invalid/canceled send")
		}
		if f.sends != 0 || f.registrations != 0 || store.state.LastReqSeq != before {
			t.Fatal("invalid send had side effects")
		}
	}
}

func TestMentionSendLostResponseIsNotRetried(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.sendErr = errors.New("lost response")
	if _, err := c.SendMentionsContext(context.Background(), "u-peer", "@Peer", "", []Mention{{Start: 0, End: 5, MID: mentionPeer}}); err == nil {
		t.Fatal("missing failure")
	}
	if f.sends != 1 {
		t.Fatalf("send attempts %d", f.sends)
	}
}

func TestMentionsDoNotDowngradeAfterMissingMembership(t *testing.T) {
	c, f, _, _ := setup(t, false)
	f.groupErr = line.ErrGroupKeyNotFound
	f.chats = &line.GetChatsResponse{}
	if _, err := c.SendMentionsContext(context.Background(), "c-group", "@Peer", "", []Mention{{Start: 0, End: 5, MID: mentionPeer}}); err == nil {
		t.Fatal("accepted missing key")
	}
	if f.sends != 0 || f.registrations != 0 {
		t.Fatal("failed preparation mutated")
	}
}
