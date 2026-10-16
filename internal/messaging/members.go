package messaging

import (
	"context"
	"errors"
	"sort"
	"strings"

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
	if ValidateChatID(chat) != nil || !strings.HasPrefix(strings.ToLower(chat), "c") {
		return errors.New("members requires a group chat; direct chats and legacy rooms are unsupported")
	}
	return nil
}

// MembershipContext does not initialize crypto or negotiate/register keys.
// Legacy rooms use a separate Chrome API whose contract is not implemented here.
func MembershipContext(ctx context.Context, manager *session.Manager, chat string) (*Membership, error) {
	if err := ValidateMembershipChat(chat); err != nil {
		return nil, err
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
