package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

type membershipAPI struct {
	session.API
	group      *line.GroupExtra
	contacts   map[string]line.ContactWrapper
	profileErr error
	batches    []int
	reads      int
}

func (f *membershipAPI) GetChatsContext(ctx context.Context, ids []string, members, invitees bool) (*line.GetChatsResponse, error) {
	f.reads++
	if len(ids) != 1 || ids[0] != "c-group" || !members || !invitees {
		return nil, errors.New("invalid membership request")
	}
	return &line.GetChatsResponse{Chats: []line.Chat{{ChatMid: "c-group", ChatName: "Team", Extra: line.ChatExtra{GroupExtra: f.group}}}}, ctx.Err()
}
func (f *membershipAPI) GetContactsV2Context(ctx context.Context, ids []string) (*line.ContactsResponse, error) {
	f.batches = append(f.batches, len(ids))
	if f.profileErr != nil {
		return nil, f.profileErr
	}
	contacts := make(map[string]line.ContactWrapper)
	for _, id := range ids {
		if c, ok := f.contacts[id]; ok {
			contacts[id] = c
		}
	}
	return &line.ContactsResponse{Contacts: contacts}, ctx.Err()
}
func membersApp(f *membershipAPI) (*App, *strings.Builder) {
	a, _, _ := testApp(nil)
	out := new(strings.Builder)
	a.Out = out
	// Missing E2EE keys must never stop this read-only command.
	a.Manager.Store = &testStore{state: &session.State{MID: "u-self", AccessToken: "synthetic"}}
	a.Manager.NewClient = func(string) session.API { return f }
	return a, out
}

func TestMembersOutputProfilesSelfAndInvitees(t *testing.T) {
	f := &membershipAPI{group: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-self": true, "u-peer": true}, InviteeMids: line.FlexibleMidMap{"u-invited": true}}, contacts: map[string]line.ContactWrapper{
		"u-peer": {Contact: line.Contact{Mid: "u-peer", DisplayName: "Original", DisplayNameOverridden: "Preferred"}},
		"u-self": {Contact: line.Contact{Mid: "u-self", DisplayName: "Self"}},
	}}
	a, out := membersApp(f)
	if err := a.Run([]string{"--error-format=json", "members", "id:c-group", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result membershipOutput
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || !result.MembersComplete || !result.InviteesComplete || len(result.Members) != 2 || len(result.Invitees) != 1 {
		t.Fatalf("bad membership %s", out.String())
	}
	if result.Members[0].MID != "u-peer" || result.Members[0].Name == nil || *result.Members[0].Name != "Preferred" || result.Members[0].Self || !result.Members[1].Self {
		t.Fatal("incorrect name/self")
	}
	if result.Invitees[0].Name != nil || result.Invitees[0].NameStatus != "unavailable" {
		t.Fatal("unavailable profile invented")
	}
	out.Reset()
	f.profileErr = errors.New("private remote error")
	if err := a.Run([]string{"members", "--json", "id:c-group"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "private") || strings.Contains(out.String(), "Preferred") || !strings.Contains(out.String(), `"name_status": "unavailable"`) {
		t.Fatal("profile failure lost membership/leaked")
	}
}

func TestMembersDistinguishesEmptyFromOmitted(t *testing.T) {
	for _, present := range []bool{false, true} {
		group := &line.GroupExtra{}
		if present {
			group.MemberMids = line.FlexibleMidMap{}
		}
		a, out := membersApp(&membershipAPI{group: group})
		if err := a.Run([]string{"members", "id:c-group", "--json"}); err != nil {
			t.Fatal(err)
		}
		var result membershipOutput
		if json.Unmarshal([]byte(out.String()), &result) != nil || result.Members == nil || result.Invitees == nil || result.MembersComplete != present || result.InviteesComplete {
			t.Fatalf("bad completeness %s", out.String())
		}
		out.Reset()
		if err := a.Run([]string{"members", "id:c-group"}); err != nil {
			t.Fatal(err)
		}
		want := "Members (complete): 0"
		if !present {
			want = "Members (incomplete; membership list unavailable): 0"
		}
		if !strings.Contains(out.String(), want) {
			t.Fatal("human output hides incomplete list")
		}
	}
}

func TestMembersValidationAndHelpBeforeStorage(t *testing.T) {
	for _, args := range [][]string{{"members"}, {"members", "id:u-peer"}, {"members", "id:r-room"}, {"members", "bad\nname"}, {"members", "id:c-group", "--timeout", "-1s"}, {"members", "id:c-group", "extra"}, {"members", "--help"}, {"--error-format=json", "members", "--help"}} {
		a, _, _ := testApp(nil)
		a.Lock = func() (func(), error) { t.Fatal("opened storage for invalid args/help"); return nil, nil }
		err := a.Run(args)
		if strings.Contains(strings.Join(args, " "), "--help") && err != nil {
			t.Fatal(err)
		}
	}
}

func TestMembersNamesAreBatchedAndCancellationPropagates(t *testing.T) {
	f := &membershipAPI{group: &line.GroupExtra{MemberMids: make(line.FlexibleMidMap)}}
	for i := 0; i < 205; i++ {
		f.group.MemberMids[fmt.Sprintf("u-%03d", i)] = true
	}
	a, _ := membersApp(f)
	if err := a.Run([]string{"members", "id:c-group", "--json"}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.batches) != "[100 100 5]" {
		t.Fatalf("bad batch sizes %v", f.batches)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Context = ctx
	before := f.reads
	if err := a.Run([]string{"members", "id:c-group", "--json"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation %v", err)
	}
	if f.reads != before {
		t.Fatal("canceled read reached API")
	}
}

func TestMembersDoesNotInventSelfOrAcceptMismatchedProfile(t *testing.T) {
	f := &membershipAPI{group: &line.GroupExtra{MemberMids: line.FlexibleMidMap{"u-peer": true}}, contacts: map[string]line.ContactWrapper{"u-peer": {Contact: line.Contact{Mid: "u-other", DisplayName: "Wrong person"}}}}
	a, out := membersApp(f)
	if err := a.Run([]string{"members", "id:c-group", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result membershipOutput
	if json.Unmarshal([]byte(out.String()), &result) != nil || len(result.Members) != 1 || result.Members[0].Self || result.Members[0].Name != nil {
		t.Fatal("invented self or misattributed profile")
	}
}
