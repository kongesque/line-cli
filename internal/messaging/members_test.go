package messaging

import (
	"context"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
)

func TestMembershipCompletenessAndNoSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		group    *line.GroupExtra
		complete bool
		count    int
	}{
		{"missing", nil, false, 0}, {"omitted", &line.GroupExtra{}, false, 0},
		{"empty", &line.GroupExtra{MemberMids: line.FlexibleMidMap{}}, true, 0},
		{"members", &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-self": true, "u-peer": true}, InviteeMids: line.FlexibleMidMap{"u-invited": true}}, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, _, store := setup(t, false)
			f.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", ChatName: "Group", Extra: line.ChatExtra{GroupExtra: tc.group}}}}
			got, err := MembershipContext(context.Background(), c.Session, "c-group")
			if err != nil {
				t.Fatal(err)
			}
			if got.MembersComplete != tc.complete || len(got.Members) != tc.count || got.ChatID != "c-group" || got.Members == nil || got.Invitees == nil {
				t.Fatalf("bad membership: %#v", got)
			}
			if tc.count == 2 && (len(got.Invitees) != 1 || got.Members[0] != "u-peer" || !got.InviteesComplete) {
				t.Fatal("member/invitee distinction lost")
			}
			if f.sends != 0 || f.registrations != 0 || store.state.LastReqSeq != 0 || len(f.lookupIDs) != 0 {
				t.Fatal("membership read had side effects")
			}
		})
	}
}

func TestMembershipRejectsInconsistentResponse(t *testing.T) {
	for _, response := range []*line.GetChatsResponse{nil, {},
		{Chats: []line.Chat{{ChatMid: "c-other"}}},
		{Chats: []line.Chat{{ChatMid: "c-group"}, {ChatMid: "c-group"}}},
		{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"c-other": true}}}}}},
	} {
		c, f, _, _ := setup(t, false)
		f.chats = response
		if _, err := MembershipContext(context.Background(), c.Session, "c-group"); err == nil {
			t.Fatal("accepted inconsistent membership")
		}
	}
	for _, chat := range []string{"", "u-peer", "r-room", "bad"} {
		if _, err := MembershipContext(context.Background(), nil, chat); err == nil {
			t.Fatal("accepted unsupported chat")
		}
	}
}
