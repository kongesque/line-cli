package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

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
	return linuxStorage{native: native, provider: func(context.Context) (sealedKeyProvider, error) { return provider, nil }}, provider, secrets
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

func TestLinuxResolverFollowsMigrationAndReusesSealedKey(t *testing.T) {
	s, provider, secrets := testLinuxStorage(t)
	state := envelopeState()
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	native, err := s.read()
	if err != nil || hasEnvelopeMarker(native) || provider.unseals != 0 || provider.seals != 0 {
		t.Fatal("native format changed", err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(state, got) {
		t.Fatal(err)
	}
	// A migration between locked operations preserves state and replaces only
	// the authoritative file. This is a fixture, not migration implementation.
	data, err := createHeadlessSession(context.Background(), state, provider, requireHost)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidate(t, s, data)
	secrets.fault = func(string, string) bool { t.Fatal("headless operation touched Secret Service"); return true }
	got, err = s.Load()
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatal("cached native selection", err)
	}
	e, _ := parseEnvelope(data)
	for i := 0; i < 3; i++ {
		got.LastReqSeq++
		*got.WatchRevision++
		got.AccessToken = "rotated-synthetic"
		if err := s.Save(got); err != nil {
			t.Fatal(err)
		}
		after, err := s.read()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parseEnvelope(after)
		if err != nil || !bytes.Equal(e.sealed, parsed.sealed) || e.protection != parsed.protection || bytes.Equal(e.nonce, parsed.nonce) {
			t.Fatal("enrolled key or policy changed", err)
		}
		loaded, err := s.Load()
		if err != nil || !reflect.DeepEqual(got, loaded) {
			t.Fatal("update lost state", err)
		}
		e = parsed
	}
	if provider.seals != 1 {
		t.Fatal("updated session was resealed")
	}
	before, _ := s.read()
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	after, _ := s.read()
	if !bytes.Equal(before, after) || provider.seals != 1 {
		t.Fatal("preflight changed active storage")
	}
	entries, err := os.ReadDir(filepath.Dir(s.native.path))
	if err != nil || len(entries) != 1 {
		t.Fatal("preflight leaked files", err)
	}
	provider.fail = ErrCredentialHelper
	if err := s.Save(state); !errors.Is(err, ErrCredentialHelper) {
		t.Fatal(err)
	}
	after, _ = s.read()
	if !bytes.Equal(before, after) {
		t.Fatal("helper failure overwrote storage")
	}
	provider.fail = nil
	if _, err := s.Load(); err != nil {
		t.Fatal("failure triggered resealing or fallback", err)
	}
}

func TestLinuxResolverRejectsUnknownCorruptAndDamagedEnvelopes(t *testing.T) {
	for _, kind := range []string{"version", "backend", "policy", "ciphertext", "marker", "sealed", "size"} {
		t.Run(kind, func(t *testing.T) {
			s, provider, _ := testLinuxStorage(t)
			// Keep a native key available to catch an unsafe fallback/overwrite.
			if err := s.Save(envelopeState()); err != nil {
				t.Fatal(err)
			}
			data, err := createHeadlessSession(context.Background(), envelopeState(), provider, requireHost)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "version":
				data[16] = 2
			case "backend":
				data[18] = 2
			case "policy":
				data[24] = 2
			case "ciphertext":
				data[len(data)-1] ^= 1
			case "marker":
				data[0] ^= 1
			case "sealed":
				data[envelopeFixedBytes] ^= 1
			case "size":
				data[20] = 255
			}
			writeCandidate(t, s, data)
			beforeCalls := provider.unseals
			if _, err := s.Load(); err == nil {
				t.Fatal("corrupt load accepted")
			}
			if err := s.Save(envelopeState()); err == nil {
				t.Fatal("corrupt state replaced")
			}
			if kind != "ciphertext" && provider.unseals != beforeCalls {
				t.Fatal("malformed header reached credential helper")
			}
			after, _ := s.read()
			if !bytes.Equal(data, after) {
				t.Fatal("file changed on failure")
			}
		})
	}
}

func TestHeadlessPreparationCleanupAndUncertainSave(t *testing.T) {
	s, provider, _ := testLinuxStorage(t)
	data, err := createHeadlessSession(context.Background(), envelopeState(), provider, requireHost)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidate(t, s, data)
	ops := defaultFileOperations()
	ops.remove = func(*os.Root, string) error { return errors.New("remove failure") }
	s.native.ops = &ops
	if err := s.Prepare(); !errors.Is(err, ErrStorageCleanup) {
		t.Fatal(err)
	}
	after, _ := s.read()
	if !bytes.Equal(after, data) {
		t.Fatal("preflight replaced session")
	}
	ops = defaultFileOperations()
	ops.syncDir = func(*os.Root) error { return errors.New("sync failure") }
	state := envelopeState()
	state.LastReqSeq++
	if err := s.Save(state); !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal(err)
	}
	s.native.ops = nil
	got, err := s.Load()
	if err != nil || got.LastReqSeq != state.LastReqSeq || provider.seals != 1 {
		t.Fatal("uncertain save rolled back or resealed", err)
	}
}

func TestNativeStoreRefusesHeadlessEnvelope(t *testing.T) {
	s, provider, _ := testLinuxStorage(t)
	data, err := createHeadlessSession(context.Background(), envelopeState(), provider, requireHost)
	if err != nil {
		t.Fatal(err)
	}
	writeCandidate(t, s, data)
	if _, err := s.native.Load(); !errors.Is(err, ErrStorageFormat) {
		t.Fatal(err)
	}
	if err := s.native.Save(envelopeState()); !errors.Is(err, ErrStorageFormat) {
		t.Fatal(err)
	}
}

// This can initialize a system host secret: explicitly enable ONLY in a
// disposable booted Linux VM. All state and keys are newly generated synthetic
// values. This test never invokes Secret Service or contacts LINE.
func TestLinuxSystemdStorageIntegration(t *testing.T) {
	if os.Getenv("LINE_CLI_TEST_SYSTEMD_CREDS") != "1" {
		t.Skip("requires an explicitly enabled disposable Linux VM")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := resolvedLinuxStorage()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newSystemdCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := envelopeState()
	data, err := createHeadlessSession(context.Background(), state, provider, requireHost)
	if err != nil {
		t.Fatal("native enrollment failed", err)
	}
	writeCandidate(t, s, data)
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(state, got) {
		t.Fatal("native roundtrip failed", err)
	}
	state.LastReqSeq++
	state.AccessToken = "rotated-synthetic"
	if err := s.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	after, _ := s.read()
	oldEnvelope, _ := parseEnvelope(data)
	newEnvelope, _ := parseEnvelope(after)
	if !bytes.Equal(oldEnvelope.sealed, newEnvelope.sealed) {
		t.Fatal("native update resealed wrapping key")
	}
	h := provider.(systemdCredentials)
	args := h.args("decrypt")
	args[1] = "--name=line-cli-wrong-name"
	wrong, err := h.run(context.Background(), args, oldEnvelope.sealed, 32)
	clear(wrong)
	if !errors.Is(err, ErrCredentialHelper) {
		t.Fatal("wrong credential name accepted", err)
	}
	// Repeated full store loads include trusted-helper discovery/version and
	// unsealing, measuring the actual uncached watcher path rather than crypto.
	start := time.Now()
	for i := 0; i < 20; i++ {
		if _, err := s.Load(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("20 uncached loads: %s (mean %s)", time.Since(start), time.Since(start)/20)
	after[len(after)-1] ^= 1
	writeCandidate(t, s, after)
	if _, err := s.Load(); !errors.Is(err, ErrStorageAuthentication) {
		t.Fatal("tampered session accepted", err)
	}
}
