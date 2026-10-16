package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/kongesque/line-cli/internal/session"

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

type membershipRecoveryAPI struct {
	*fakeAPI
	calls int
}

func (f *membershipRecoveryAPI) GetChatsContext(ctx context.Context, ids []string, members, invitees bool) (*line.GetChatsResponse, error) {
	f.calls++
	if f.calls == 1 {
		return nil, errors.New(`{"code":119}`)
	}
	if f.store.state.AccessToken != "refreshed" {
		return nil, errors.New("retry before persisted token")
	}
	return f.fakeAPI.GetChatsContext(ctx, ids, members, invitees)
}
func (f *membershipRecoveryAPI) RefreshAccessToken(string) (*line.TokenV3IssueResult, error) {
	return &line.TokenV3IssueResult{AccessToken: "refreshed", RefreshToken: "rotated"}, nil
}
func TestMembershipReadRecoversAuthentication(t *testing.T) {
	c, f, _, store := setup(t, false)
	store.state.RefreshToken = "refresh"
	f.chats = &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", Extra: line.ChatExtra{GroupExtra: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-peer": true}}}}}}
	api := &membershipRecoveryAPI{fakeAPI: f}
	c.Session.NewClient = func(string) session.API { return api }
	result, err := MembershipContext(context.Background(), c.Session, "c-group")
	if err != nil || api.calls != 2 || !result.MembersComplete || f.registrations != 0 || store.state.LastReqSeq != 0 {
		t.Fatalf("membership recovery failed: %v", err)
	}
}
