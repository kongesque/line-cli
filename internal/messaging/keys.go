package messaging

import (
	"errors"
	"fmt"
	"sort"

	"github.com/kongesque/line-cli/internal/session"
	gen "github.com/kongesque/line-cli/pkg"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
)

var (
	errNoLetterSealing = errors.New("LINE explicitly reports Letter Sealing unavailable")
	errGroupKeyMissing = errors.New("group shared key not registered")
)

func validatePeer(key *line.E2EEPublicKey, expected int) error {
	if key == nil || key.PublicKey == "" {
		return errors.New("LINE returned no peer public key")
	}
	id, err := key.KeyID.Int64()
	if err != nil || id <= 0 || id > 0xffffffff || (expected > 0 && id != int64(expected)) {
		return errors.New("LINE returned an unexpected peer key ID")
	}
	if _, err := gen.NormalizePeerPublicKeyB64(key.PublicKey); err != nil {
		return errors.New("LINE returned an invalid peer public key")
	}
	return nil
}

func (c *Client) negotiate(mid string) (*line.E2EEPublicKey, error) {
	var key *line.E2EEPublicKey
	err := c.Session.Do(func(api session.API) (err error) { key, err = api.NegotiateE2EEPublicKey(mid); return })
	if line.IsE2EEDisabled(session.ProtocolError(err)) {
		return nil, errNoLetterSealing
	}
	if err != nil {
		return nil, err
	}
	if err := validatePeer(key, 0); err != nil {
		return nil, err
	}
	return key, nil
}

func (c *Client) peerByID(mid string, id int) error {
	if c.crypto.IsMyKey(id) || c.crypto.HasPeerPublicKey(id) {
		return nil
	}
	var key *line.E2EEPublicKey
	if err := c.Session.Do(func(api session.API) (err error) { key, err = api.GetE2EEPublicKey(mid, 1, id); return }); err != nil {
		return errors.New("required peer device key could not be retrieved")
	}
	if err := validatePeer(key, id); err != nil {
		return err
	}
	c.crypto.RegisterPeerPublicKey(id, key.PublicKey)
	return nil
}

// Historical reads never create/replace a group key. A sender may register a
// fresh key explicitly as part of preparation for a requested send.
func (c *Client) groupKey(chat string, id int) error {
	cacheKey := fmt.Sprintf("%s:%d", chat, id)
	if id > 0 && c.groupKeys[cacheKey] {
		return nil
	}
	var key *line.E2EEGroupSharedKey
	err := c.Session.Do(func(api session.API) (err error) {
		if id > 0 {
			key, err = api.GetE2EEGroupSharedKey(chat, id)
		} else {
			key, err = api.GetLastE2EEGroupSharedKey(chat)
		}
		return
	})
	protocolErr := session.ProtocolError(err)
	if line.IsGroupKeyNotFound(protocolErr) {
		return errGroupKeyMissing
	}
	if line.IsE2EEDisabled(protocolErr) {
		return errNoLetterSealing
	}
	if err != nil {
		return err
	}
	if key == nil || key.GroupKeyID <= 0 || key.CreatorKeyID <= 0 || key.ReceiverKeyID <= 0 || key.EncryptedSharedKey == "" || (id > 0 && key.GroupKeyID != id) {
		return errors.New("LINE returned an incomplete or mismatched group key")
	}
	if err := c.peerByID(key.Creator, key.CreatorKeyID); err != nil {
		return err
	}
	if _, err := c.crypto.UnwrapGroupSharedKey(chat, key); err != nil {
		if errors.Is(err, e2ee.ErrMissingOwnPrivateKey) {
			return e2ee.ErrMissingOwnPrivateKey
		}
		return errors.New("could not unwrap the group key")
	}
	c.groupKeys[fmt.Sprintf("%s:%d", chat, key.GroupKeyID)] = true
	return nil
}

func (c *Client) registerGroupKey(chat string) error {
	var response *line.GetChatsResponse
	if err := c.Session.Do(func(api session.API) (err error) { response, err = api.GetChats([]string{chat}, true, true); return }); err != nil {
		return err
	}
	var group *line.GroupExtra
	if response != nil {
		for _, item := range response.Chats {
			if item.ChatMid == chat {
				group = item.Extra.GroupExtra
			}
		}
	}
	// The upstream Matrix fallback is unavailable to a standalone CLI. Refuse
	// partial membership rather than register a key that excludes participants.
	if group == nil || len(group.MemberMids) == 0 {
		return errors.New("complete group membership unavailable; cannot register a group key")
	}
	members := make([]string, 0, len(group.MemberMids))
	for mid := range group.MemberMids {
		if err := ValidateChatID(mid); err != nil || toType(mid) != 0 {
			return errors.New("invalid group member ID")
		}
		if mid != c.state.MID {
			members = append(members, mid)
		}
	}
	// LINE may omit the caller from MemberMids. Add it explicitly, but reject
	// empty/self-only maps: the CLI has no other source for membership.
	if len(members) == 0 {
		return errors.New("complete group membership unavailable; cannot register a group key")
	}
	sort.Strings(members)
	keyIDs := make([]int, 0, len(members)+1)
	pubs := make([]string, 0, len(members)+1)
	for _, mid := range members {
		key, err := c.negotiate(mid)
		if err != nil {
			return err
		}
		id, _ := key.KeyID.Int64()
		keyIDs = append(keyIDs, int(id))
		pubs = append(pubs, key.PublicKey)
	}
	ownID, ownPub, err := c.crypto.MyPublicKey()
	if err != nil {
		return errors.New("own key unavailable for group registration")
	}
	members, keyIDs, pubs = append(members, c.state.MID), append(keyIDs, ownID), append(pubs, ownPub)
	generated, err := c.crypto.GenerateGroupKey()
	if err != nil {
		return errors.New("could not generate group key")
	}
	wrapped := make([]string, len(members))
	for i, pub := range pubs {
		wrapped[i], err = c.crypto.WrapGroupKeyForMember(pub, generated)
		if err != nil {
			return errors.New("could not wrap group key for every member")
		}
	}
	return c.Session.Mutate(func(api session.API) error { return api.RegisterE2EEGroupKey(1, chat, members, keyIDs, wrapped) })
}
