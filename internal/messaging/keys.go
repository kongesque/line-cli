package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/kongesque/line-cli/internal/fault"
	"github.com/kongesque/line-cli/internal/session"
	gen "github.com/kongesque/line-cli/pkg"
	"github.com/kongesque/line-cli/pkg/e2ee"
	"github.com/kongesque/line-cli/pkg/line"
)

var (
	errNoLetterSealing = errors.New("LINE explicitly reports Letter Sealing unavailable")
	errGroupKeyMissing = errors.New("group shared key not registered")
	keyRetryBackoffs   = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
)

const keyRetryCooldown = 30 * time.Second

func (c *Client) sleep(ctx context.Context, delay time.Duration) error {
	if c.wait != nil {
		return c.wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// doKeyFetchWithRetry retries read-only E2EE key fetches on transient transport
// or 5xx/429/408 errors with bounded backoff and a per-client cooldown so a
// sustained outage does not stall history or watch decoding. Auth errors and
// mutations are never retried.
func (c *Client) doKeyFetchWithRetry(ctx context.Context, fn func(session.API) error) error {
	err := c.Session.DoContext(ctx, fn)
	if err == nil {
		c.keyRetrySkipUntil = time.Time{}
		return nil
	}
	if ctx.Err() != nil || !line.IsTransientError(session.ProtocolError(err)) {
		return err
	}
	if now := c.Session.Now(); !c.keyRetrySkipUntil.IsZero() && now.Before(c.keyRetrySkipUntil) {
		return err
	}
	for _, delay := range keyRetryBackoffs {
		if waitErr := c.sleep(ctx, delay); waitErr != nil {
			return waitErr
		}
		err = c.Session.DoContext(ctx, fn)
		if err == nil {
			c.keyRetrySkipUntil = time.Time{}
			return nil
		}
		if ctx.Err() != nil || !line.IsTransientError(session.ProtocolError(err)) {
			return err
		}
	}
	c.keyRetrySkipUntil = c.Session.Now().Add(keyRetryCooldown)
	return err
}

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

func (c *Client) negotiate(ctx context.Context, mid string) (*line.E2EEPublicKey, error) {
	var key *line.E2EEPublicKey
	err := c.Session.DoContext(ctx, func(api session.API) (err error) { key, err = api.NegotiateE2EEPublicKeyContext(ctx, mid); return })
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

func (c *Client) peerByID(ctx context.Context, mid string, id int) error {
	if c.crypto.IsMyKey(id) || c.crypto.HasPeerPublicKey(id) {
		return nil
	}
	var key *line.E2EEPublicKey
	if err := c.doKeyFetchWithRetry(ctx, func(api session.API) (err error) { key, err = api.GetE2EEPublicKeyContext(ctx, mid, 1, id); return }); err != nil {
		return fmt.Errorf("required peer device key could not be retrieved: %w", err)
	}
	if err := validatePeer(key, id); err != nil {
		return err
	}
	c.crypto.RegisterPeerPublicKey(id, key.PublicKey)
	return nil
}

// Historical reads never create/replace a group key. A sender may register a
// fresh key explicitly as part of preparation for a requested send.
func (c *Client) groupKey(ctx context.Context, chat string, id int) error {
	cacheKey := fmt.Sprintf("%s:%d", chat, id)
	if id > 0 {
		if _, cached := c.groupKeys[cacheKey]; cached {
			if c.crypto.HasGroupKey(chat, id) {
				return nil
			}
			delete(c.groupKeys, cacheKey)
		}
	}
	var key *line.E2EEGroupSharedKey
	err := c.doKeyFetchWithRetry(ctx, func(api session.API) (err error) {
		if id > 0 {
			key, err = api.GetE2EEGroupSharedKeyContext(ctx, chat, id)
		} else {
			key, err = api.GetLastE2EEGroupSharedKeyContext(ctx, chat)
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
	version := key.SpecVersion
	if version == 0 {
		// Chrome 3.7.2 defaults absent negotiation versions to Nd (2).
		version = 2
	}
	if id == 0 && version != 1 && version != 2 {
		return errors.New("unsupported negotiated group encryption version; nothing was sent")
	}
	if err := c.peerByID(ctx, key.Creator, key.CreatorKeyID); err != nil {
		return err
	}
	if _, err := c.crypto.UnwrapGroupSharedKey(chat, key); err != nil {
		var missingOwn *e2ee.MissingOwnKeyError
		if errors.As(err, &missingOwn) {
			return missingOwn
		}
		if errors.Is(err, e2ee.ErrMissingOwnPrivateKey) {
			return e2ee.ErrMissingOwnPrivateKey
		}
		return errors.New("could not unwrap the group key")
	}
	c.groupKeys[fmt.Sprintf("%s:%d", chat, key.GroupKeyID)] = version
	if id == 0 {
		c.groupKeys[cacheKey] = version
	}
	return nil
}

func (c *Client) registerGroupKey(ctx context.Context, chat string, op *fault.Mutation) error {
	var peerKeys map[string]line.E2EEPeerPublicKey
	err := c.Session.DoContext(ctx, func(api session.API) (err error) {
		peerKeys, err = api.GetLastE2EEPublicKeysContext(ctx, chat)
		return
	})
	if line.IsE2EEDisabled(session.ProtocolError(err)) {
		return errNoLetterSealing
	}
	if err != nil {
		return err
	}
	// LINE's getLastE2EEPublicKeys(chatMid) is the authoritative member key set.
	// Refuse empty maps or maps missing the caller rather than inventing membership.
	if len(peerKeys) == 0 {
		return errors.New("complete group membership unavailable; cannot register a group key")
	}
	callerKey, hasCaller := peerKeys[c.state.MID]
	if !hasCaller {
		return errors.New("caller missing from authoritative group key list; cannot register a group key")
	}
	if callerKey.KeyID <= 0 || !c.crypto.IsMyKey(callerKey.KeyID) {
		return fault.New("invalid_session", "preparation", "saved Letter Sealing keys are out of date; run line login", nil)
	}
	members := make([]string, 0, len(peerKeys))
	for mid := range peerKeys {
		if err := ValidateChatID(mid); err != nil || toType(mid) != 0 {
			return errors.New("invalid group member ID")
		}
		members = append(members, mid)
	}
	sort.Strings(members)
	keyIDs := make([]int, 0, len(members))
	pubs := make([]string, 0, len(members))
	for _, mid := range members {
		entry := peerKeys[mid]
		if entry.KeyID <= 0 || entry.KeyID > 0xffffffff {
			return errors.New("LINE returned an unexpected member key ID")
		}
		if _, err := gen.NormalizePeerPublicKeyB64(entry.KeyData); err != nil {
			return errors.New("LINE returned an invalid member public key")
		}
		keyIDs = append(keyIDs, entry.KeyID)
		pubs = append(pubs, entry.KeyData)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	wrapped, err := c.crypto.WrapRegistrationGroupKey(pubs)
	if err != nil {
		return errors.New("could not wrap group key for every member")
	}
	if len(wrapped) != len(members) {
		return errors.New("could not wrap group key for every member")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var registrationErr error
	err = c.attempt(ctx, op, "group_key_registration", false, func(api session.API) error {
		registrationErr = api.RegisterE2EEGroupKeyContext(ctx, 1, chat, members, keyIDs, wrapped)
		return registrationErr
	})
	// Classify the RPC reply directly because session errors can include credential recovery.
	var responseErr *line.ResponseError
	if errors.As(registrationErr, &responseErr) &&
		(responseErr.Status == http.StatusOK || responseErr.Status == http.StatusBadRequest) {
		if line.IsE2EEDisabled(responseErr) {
			op.Steps[len(op.Steps)-1].Outcome = fault.Rejected
			return errNoLetterSealing
		}
		if line.IsE2EEGroupMemberMismatch(responseErr) {
			op.Steps[len(op.Steps)-1].Outcome = fault.Rejected
			return fault.New("group_member_mismatch", "group_key_registration", "group membership changed during key registration; nothing was sent; retry line send", registrationErr)
		}
	}
	if err != nil {
		return fmt.Errorf("group-key registration did not return success; it may have completed; inspect LINE before retrying: %w", err)
	}
	return nil
}
