package e2ee

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kongesque/line-cli/pkg/line"
	"github.com/kongesque/line-cli/pkg/ltsm"
)

func TestIsFatalLTSMError(t *testing.T) {
	if !isFatalLTSMError(ltsm.ErrAbort) {
		t.Fatal("direct LTSM abort was not classified as fatal")
	}
	if !isFatalLTSMError(fmt.Errorf("decrypt failed: %w", ltsm.ErrAbort)) {
		t.Fatal("wrapped LTSM abort was not classified as fatal")
	}
	if !isFatalLTSMError(fmt.Errorf("V2 failed: %w; V1 fallback failed: %w", errors.New("invalid V2 ciphertext"), ltsm.ErrAbort)) {
		t.Fatal("LTSM abort in a combined fallback error was not classified as fatal")
	}
	if isFatalLTSMError(errors.New("authentication failed")) {
		t.Fatal("ordinary decryption error was classified as fatal")
	}
}

func TestGroupEncryptionV1RoundTrip(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := m.runner.KeyGenerate()
	if err != nil {
		t.Fatal(err)
	}
	public, err := m.runner.KeyGetPublic(sender)
	if err != nil {
		t.Fatal(err)
	}
	group, err := m.runner.KeyGenerate()
	if err != nil {
		t.Fatal(err)
	}
	const chat = "c00000000000000000000000000000000"
	const from = "u11111111111111111111111111111111"
	m.myKeyID, m.myRawKeyID = sender, 11
	m.peerPublic[11] = public
	m.groupKeys[chat] = map[int]int{33: group}
	m.latestGroupKey[chat] = 33
	chunks, err := m.EncryptGroupMessageVersion(chat, from, 1, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 5 {
		t.Fatal("invalid chunks")
	}
	salt, err := base64.StdEncoding.DecodeString(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	mac, err := base64.StdEncoding.DecodeString(chunks[2])
	if err != nil {
		t.Fatal(err)
	}
	if len(salt) != 8 || len(mac) != 16 {
		t.Fatal("cipher framing is not V1")
	}
	payload, key, err := m.DecryptGroupMessage(&line.Message{From: from, To: chat, ToType: 2, Chunks: chunks, ContentMetadata: map[string]string{"e2eeVersion": "1"}}, chat)
	if err != nil || key != 33 || payload != `{"text":"synthetic"}` {
		t.Fatalf("V1 roundtrip failed: %v", err)
	}
	for _, version := range []int{0, 3, -1} {
		if _, err := m.EncryptGroupMessageVersion(chat, from, version, "synthetic"); err == nil {
			t.Fatal("unsupported version accepted")
		}
	}
	// Force a V2 runner failure after channel preparation. Preserve that error;
	// the former fallback replaced it with a "V1 encrypt failed" error.
	m.groupSessionChannels[chat][33][11] = -1
	if chunks, err := m.EncryptGroupMessageVersion(chat, from, 2, "synthetic"); err == nil || err.Error() != "unknown channel: -1" || chunks != nil {
		t.Fatalf("V2 failure fell back or returned chunks: %v", err)
	}
}

func TestUnwrapGroupSharedKeyReturnsMissingOwnPrivateKey(t *testing.T) {
	manager := &Manager{
		peerPublic: map[int]string{1234: "creator-public-key"},
		keyByRawID: map[int]int{},
	}

	_, err := manager.UnwrapGroupSharedKey("c-group", &line.E2EEGroupSharedKey{
		CreatorKeyID:  1234,
		ReceiverKeyID: 5625926,
	})
	if !errors.Is(err, ErrMissingOwnPrivateKey) {
		t.Fatalf("err = %v, want ErrMissingOwnPrivateKey", err)
	}
	var missing *MissingOwnKeyError
	if !errors.As(err, &missing) || missing.KeyID != 5625926 {
		t.Fatalf("err = %v, want *MissingOwnKeyError{KeyID: 5625926}", err)
	}
}

func TestChannelFromKeyIDsReturnsMissingOwnPrivateKeyWhenPeerKeyKnown(t *testing.T) {
	manager := &Manager{
		peerPublic: map[int]string{1513671: "peer-public-key"},
		keyByRawID: map[int]int{},
	}

	_, err := manager.channelFromKeyIDs(1513671, 5920082)
	if !errors.Is(err, ErrMissingOwnPrivateKey) {
		t.Fatalf("err = %v, want ErrMissingOwnPrivateKey", err)
	}
	var missing *MissingOwnKeyError
	if !errors.As(err, &missing) || missing.KeyID != 5920082 {
		t.Fatalf("err = %v, want *MissingOwnKeyError{KeyID: 5920082}", err)
	}
	if !strings.Contains(err.Error(), "5920082") {
		t.Fatalf("err = %v, want missing key ID in error", err)
	}
}

func TestWrapRegistrationGroupKeyReleasesTemporaryHandles(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	ownKey, err := m.runner.KeyGenerate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.ReleaseGroupKey(ownKey) }()
	ownPub, err := m.runner.KeyGetPublic(ownKey)
	if err != nil {
		t.Fatal(err)
	}
	peerKey, err := m.runner.KeyGenerate()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.ReleaseGroupKey(peerKey) }()
	peerPub, err := m.runner.KeyGetPublic(peerKey)
	if err != nil {
		t.Fatal(err)
	}
	m.myKeyID = ownKey
	m.myRawKeyID = 11
	m.myPublicB64 = ownPub

	wrapped, err := m.WrapRegistrationGroupKey([]string{ownPub, peerPub})
	if err != nil {
		t.Fatalf("WrapRegistrationGroupKey failed: %v", err)
	}
	if len(wrapped) != 2 || wrapped[0] == "" || wrapped[1] == "" {
		t.Fatalf("unexpected wrapped output: %#v", wrapped)
	}
}

func TestManagerLRUBoundsPeerAndGroupCaches(t *testing.T) {
	m := &Manager{
		channelByPair:        make(map[string]int),
		groupKeys:            make(map[string]map[int]int),
		groupSessionChannels: make(map[string]map[int]map[int]int),
		latestGroupKey:       make(map[string]int),
	}
	for i := 0; i < maxCachedPeerChannels+10; i++ {
		m.putPeerChannelLocked(fmt.Sprintf("1|%d", i), i+1)
	}
	if len(m.channelByPair) != maxCachedPeerChannels || len(m.peerChannelLRU) != maxCachedPeerChannels {
		t.Fatalf("peer channel cache unbounded: map=%d lru=%d", len(m.channelByPair), len(m.peerChannelLRU))
	}
	for i := 0; i < maxCachedGroupKeys+10; i++ {
		m.putGroupKeyLocked(fmt.Sprintf("c-%d", i), i+1, i+100)
	}
	if len(m.groupKeyLRU) != maxCachedGroupKeys {
		t.Fatalf("group key cache unbounded: lru=%d", len(m.groupKeyLRU))
	}
	for i := 0; i < maxCachedGroupChannels+10; i++ {
		m.putGroupChannelLocked(groupChannelCacheID{chatMid: "c-1", groupKeyID: 1, senderKeyID: i}, i+1)
	}
	if len(m.groupChannelLRU) != maxCachedGroupChannels {
		t.Fatalf("group channel cache unbounded: lru=%d", len(m.groupChannelLRU))
	}
}

func TestPutGroupKeyEvictingSameChatDoesNotPanic(t *testing.T) {
	m := &Manager{
		groupKeys:            make(map[string]map[int]int),
		groupSessionChannels: make(map[string]map[int]map[int]int),
		latestGroupKey:       make(map[string]int),
	}
	// Fill cache to capacity with c-0 as the oldest entry (only 1 key for c-0).
	for i := 0; i < maxCachedGroupKeys; i++ {
		m.putGroupKeyLocked(fmt.Sprintf("c-%d", i), 1, i+100)
	}
	if !m.HasGroupKey("c-0", 1) {
		t.Fatal("expected c-0:1 to be resident before eviction")
	}
	// Inserting a new key for c-0 evicts c-0:1 (deleting m.groupKeys["c-0"]) and
	// must re-create the inner map rather than panicking on a nil map write.
	m.putGroupKeyLocked("c-0", 2, 999)
	if m.HasGroupKey("c-0", 1) {
		t.Fatal("expected c-0:1 to be evicted")
	}
	if !m.HasGroupKey("c-0", 2) || m.groupKeys["c-0"][2] != 999 {
		t.Fatalf("expected c-0:2=999 after same-chat eviction, got map=%v", m.groupKeys["c-0"])
	}
	if len(m.groupKeyLRU) != maxCachedGroupKeys {
		t.Fatalf("lru length = %d, want %d", len(m.groupKeyLRU), maxCachedGroupKeys)
	}
}

func TestEncryptGroupMessageRawReturnsGroupKeyNotLoaded(t *testing.T) {
	manager := &Manager{
		sequence:       map[string]int{},
		groupKeys:      map[string]map[int]int{},
		latestGroupKey: map[string]int{},
	}

	_, err := manager.EncryptGroupMessageRaw("c-group", "u-sender", 0, []byte(`{"text":"hello"}`))
	if !errors.Is(err, ErrGroupKeyNotLoaded) {
		t.Fatalf("err = %v, want ErrGroupKeyNotLoaded", err)
	}
}

func TestManagerInvalidatesCachedHandlesAfterRunnerReset(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	m.putGroupKeyLocked("c-group", 33, 500)
	m.latestGroupKey["c-group"] = 33
	m.putPeerChannelLocked("11|22", 600)
	m.putGroupChannelLocked(groupChannelCacheID{chatMid: "c-group", groupKeyID: 33, senderKeyID: 11}, 700)

	if !m.HasGroupKey("c-group", 33) {
		t.Fatal("expected group key resident before runtime reset")
	}

	// Simulate an LTSM abort resetting the messaging runtime.
	if err := m.runner.ResetRuntime(); err != nil {
		t.Fatalf("ResetRuntime failed: %v", err)
	}

	// Manager must detect the epoch change and drop all stale native handles
	// rather than reusing dead handle IDs.
	if m.HasGroupKey("c-group", 33) {
		t.Fatal("stale group key remained resident after runtime reset")
	}
	if _, err := m.EncryptGroupMessageRaw("c-group", "u-sender", 0, []byte(`{"text":"hello"}`)); !errors.Is(err, ErrGroupKeyNotLoaded) {
		t.Fatalf("expected ErrGroupKeyNotLoaded after runtime reset, got %v", err)
	}
	m.mu.Lock()
	stalePeers := len(m.channelByPair)
	staleGroupChans := len(m.groupSessionChannels)
	m.mu.Unlock()
	if stalePeers != 0 || staleGroupChans != 0 {
		t.Fatalf("stale channels survived reset: peer=%d group=%d", stalePeers, staleGroupChans)
	}
}
