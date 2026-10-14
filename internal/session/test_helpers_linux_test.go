package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
)

// Shared synthetic fixtures for package tests.
type fakeSealedKeys struct {
	keys           map[string][]byte
	seals, unseals int
	fail           error
}

func (p *fakeSealedKeys) Seal(_ context.Context, key []byte, required protectionRequirement) ([]byte, credentialProtection, error) {
	p.seals++
	if p.fail != nil {
		return nil, credentialProtection{}, p.fail
	}
	raw := fixture(hostUserID)
	if _, err := rand.Read(raw[32:44]); err != nil {
		return nil, credentialProtection{}, err
	}
	blob := encode(raw)
	policy, _ := inspectCredential(blob)
	p.keys[string(blob)] = bytes.Clone(key)
	return blob, policy, nil
}

func (p *fakeSealedKeys) Unseal(_ context.Context, blob []byte, expected credentialProtection) ([]byte, error) {
	p.unseals++
	if p.fail != nil {
		return nil, p.fail
	}
	key, ok := p.keys[string(blob)]
	if !ok {
		return nil, ErrCredentialHelper
	}
	policy, err := inspectCredential(blob)
	if err != nil || policy != expected {
		return nil, ErrCredentialPolicy
	}
	return bytes.Clone(key), nil
}

func testLinuxStorage(t *testing.T) (linuxStorage, *fakeSealedKeys, *fakeSecretService) {
	t.Helper()
	native, secrets := syntheticFileStore(t)
	provider := &fakeSealedKeys{keys: map[string][]byte{}}
	return linuxStorage{native: native, provider: func(context.Context) (sealedKeyProvider, error) { return provider, nil }, cleanupGuard: func() error { return nil }}, provider, secrets
}

func writeCandidate(t *testing.T, s linuxStorage, data []byte) {
	t.Helper()
	files, err := s.native.files(true)
	if err != nil {
		t.Fatal(err)
	}
	defer files.root.Close()
	if err := files.replace(filepath.Base(s.native.path), data); err != nil {
		t.Fatal(err)
	}
}
