package messaging

import (
	"context"
	"errors"
	"sort"

	"github.com/kongesque/line-cli/internal/session"
	"github.com/kongesque/line-cli/pkg/line"
)

// Membership is a read-only snapshot. Completeness means the requested map was
// present, not that profiles are available or membership cannot change later.
type Membership struct {
	ChatID           string
	Name             string
	Members          []string
	Invitees         []string
	MembersComplete  bool
	InviteesComplete bool
}

func ValidateMembershipChat(chat string) error {
	if ValidateChatID(chat) != nil || (toType(chat) != 2 && toType(chat) != 1) {
		return errors.New("members requires a group or room chat; direct chats are unsupported")
	}
	return nil
}

// MembershipContext does not initialize crypto or negotiate/register keys.
// Groups (c...) use GetChats; legacy multi-person rooms (r...) use GetRoomsV2.
func MembershipContext(ctx context.Context, manager *session.Manager, chat string) (*Membership, error) {
	if err := ValidateMembershipChat(chat); err != nil {
		return nil, err
	}
	if toType(chat) == 1 {
		return roomMembershipContext(ctx, manager, chat)
	}
	var response *line.GetChatsResponse
	if err := manager.DoContext(ctx, func(api session.API) (err error) {
		response, err = api.GetChatsContext(ctx, []string{chat}, true, true)
		return
	}); err != nil {
		return nil, err
	}
	if response == nil || len(response.Chats) != 1 || response.Chats[0].ChatMid != chat {
		return nil, errors.New("LINE returned missing or inconsistent group membership")
	}
	item := response.Chats[0]
	result := &Membership{ChatID: chat, Name: item.ChatName, Members: []string{}, Invitees: []string{}}
	group := item.Extra.GroupExtra
	if group == nil {
		return result, nil
	}
	var err error
	result.Members, err = membershipIDs(group.MemberMids)
	if err != nil {
		return nil, err
	}
	result.Invitees, err = membershipIDs(group.InviteeMids)
	if err != nil {
		return nil, err
	}
	result.MembersComplete = group.MemberMids != nil
	result.InviteesComplete = group.InviteeMids != nil
	return result, nil
}

func roomMembershipContext(ctx context.Context, manager *session.Manager, chat string) (*Membership, error) {
	var rooms []line.Room
	if err := manager.DoContext(ctx, func(api session.API) (err error) {
		rooms, err = api.GetRoomsV2Context(ctx, []string{chat})
		return
	}); err != nil {
		return nil, err
	}
	if len(rooms) != 1 || (rooms[0].Mid != "" && rooms[0].Mid != chat) {
		return nil, errors.New("LINE returned missing or inconsistent room membership")
	}
	room := rooms[0]
	result := &Membership{
		ChatID:           chat,
		Members:          []string{},
		Invitees:         []string{},
		InviteesComplete: true,
	}
	mids := room.MemberMids
	if mids == nil && len(room.Contacts) > 0 {
		mids = make(line.FlexibleMidMap, len(room.Contacts))
		for _, c := range room.Contacts {
			if c.Mid != "" {
				mids[c.Mid] = true
			}
		}
	}
	if mids != nil {
		members, err := membershipIDs(mids)
		if err != nil {
			return nil, err
		}
		result.Members = members
		result.MembersComplete = true
	}
	return result, nil
}

func membershipIDs(mids line.FlexibleMidMap) ([]string, error) {
	ids := make([]string, 0, len(mids))
	for mid := range mids {
		if ValidateChatID(mid) != nil || toType(mid) != 0 {
			return nil, errors.New("LINE returned an invalid group member ID")
		}
		ids = append(ids, mid)
	}
	sort.Strings(ids)
	return ids, nil
}
