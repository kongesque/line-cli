package line

import (
	"bytes"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"golang.org/x/crypto/curve25519"

	"github.com/kongesque/line-cli/pkg/ltsm"
)

func newQRTestRunner(t *testing.T) *Runner {
	t.Helper()
	rt, err := ltsm.NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return &Runner{rt: rt, keyStore: make(map[int]uint32), nextID: 1}
}

func TestQRLoginKeyLifecycle(t *testing.T) {
	r := newQRTestRunner(t)
	lease, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if _, err := r.BeginQRLoginKey(); !errors.Is(err, ErrLoginKeyBusy) {
		t.Fatal("second login acquired the active lease")
	}
	var previous string
	for attempt := 0; attempt < 3; attempt++ {
		publicKey, err := lease.Generate()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(publicKey)
		if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != publicKey {
			t.Fatal("public key was not a 32-byte standard base64 value")
		}
		if publicKey == previous || r.loginCurveKey == 0 {
			t.Fatal("regeneration reused a public key or lost its private handle")
		}
		previous = publicKey
		if _, err := r.GenerateE2EESecret(); !errors.Is(err, ErrLoginKeyBusy) {
			t.Fatal("email login replaced an active QR key")
		}
		if err := r.ClearLoginKey(); !errors.Is(err, ErrLoginKeyBusy) {
			t.Fatal("unrelated cleanup replaced an active QR key")
		}
		active, err := r.rt.Curve25519KeyGetPublicKey(r.loginCurveKey)
		if err != nil || !bytes.Equal(active, decoded) {
			t.Fatal("active key changed during rejected operations")
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if r.loginCurveKey != 0 || r.qrLoginOwner != nil {
		t.Fatal("close left a key or ownership behind")
	}
	if err := lease.Close(); err != nil {
		t.Fatal("close was not idempotent")
	}
	if _, err := lease.Generate(); !errors.Is(err, ErrQRLoginKeyClosed) {
		t.Fatal("closed lease generated a key")
	}
	// Neither a stale Close nor stale Generate can affect a successor's key.
	next, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err := next.Generate(); err != nil {
		t.Fatal(err)
	}
	handle := r.loginCurveKey
	if err := lease.Close(); err != nil || r.loginCurveKey != handle || r.qrLoginOwner != next {
		t.Fatal("stale cleanup changed the successor")
	}
}

func TestQRLoginKeyMissingAndFailedGeneration(t *testing.T) {
	r := newQRTestRunner(t)
	server := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := r.LoginUnwrapKeyChain(server, ""); err == nil {
		t.Fatal("unwrapped a keychain without a login key")
	}
	lease, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Generate(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LoginUnwrapKeyChain(server, ""); err == nil {
		t.Fatal("unwrapped a keychain after closing the login key")
	}
	// Exercise native failure recovery without exposing private data. A new
	// lease remains reserved after failed generation until explicitly closed.
	broken := &Runner{}
	failed, err := broken.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	if publicKey, err := failed.Generate(); err == nil || publicKey != "" || broken.loginCurveKey != 0 {
		t.Fatal("failed native generation retained an active key")
	}
	if err := failed.Close(); err != nil || broken.qrLoginOwner != nil {
		t.Fatal("failed generation could not release its lease")
	}
}

func TestQRLoginKeyRetainsECDHPrivateKey(t *testing.T) {
	r := newQRTestRunner(t)
	lease, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	publicB64, err := lease.Generate()
	if err != nil {
		t.Fatal(err)
	}
	public, _ := base64.StdEncoding.DecodeString(publicB64)
	// A synthetic peer uses independent pure Go X25519 and V1 encryption.
	// Decrypting with the native active login key proves that the public value
	// returned for the QR belongs to the private key retained for key export.
	peerPrivate := bytes.Repeat([]byte{0x42}, 32)
	peerPublic, err := curve25519.X25519(peerPrivate, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := ltsm.NewChannel(peerPrivate, public)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("synthetic QR key agreement")
	ciphertext, err := peer.EncryptV1(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := r.rt.Curve25519KeyCreateChannel(r.loginCurveKey, peerPublic)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.decryptV1PanicSafe(channel, ciphertext)
	if err != nil || !bytes.Equal(decoded, plaintext) {
		t.Fatal("active QR key could not decrypt synthetic peer ciphertext")
	}
}

func TestQRLoginKeyConcurrentOwnership(t *testing.T) {
	r := newQRTestRunner(t)
	owner, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Generate(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			if _, err := r.BeginQRLoginKey(); !errors.Is(err, ErrLoginKeyBusy) {
				t.Error("concurrent QR flow acquired ownership")
			}
			if _, err := r.GenerateE2EESecret(); !errors.Is(err, ErrLoginKeyBusy) {
				t.Error("concurrent email flow acquired ownership")
			}
			if err := r.ClearLoginKey(); !errors.Is(err, ErrLoginKeyBusy) {
				t.Error("concurrent cleanup acquired ownership")
			}
		})
	}
	wg.Wait()
}

func TestQRLoginKeyDoesNotReplaceUnownedKey(t *testing.T) {
	r := newQRTestRunner(t)
	// The legacy email path also sets loginCurveKey without a QR lease.
	ptr, err := r.rt.Curve25519KeyGenerate()
	if err != nil {
		t.Fatal(err)
	}
	r.loginCurveKey = ptr
	if _, err := r.BeginQRLoginKey(); !errors.Is(err, ErrLoginKeyBusy) || r.loginCurveKey != ptr {
		t.Fatal("QR replaced a key owned by the legacy login path")
	}
	if err := r.ClearLoginKey(); err != nil || r.loginCurveKey != 0 {
		t.Fatal("unowned login key was not cleared")
	}
	if err := r.ClearLoginKey(); err != nil {
		t.Fatal("clearing an absent login key failed")
	}
	lease, err := r.BeginQRLoginKey()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
}

func TestClearEmailLoginKeyPreservesRunnerSeed(t *testing.T) {
	r := newQRTestRunner(t)
	// Public bundled library token, identical to the runner's default; no account
	// credentials or saved session data are used by this test.
	const libraryToken = "wODdrvWqmdP4Zliay-iF3cz3KZcK0ekrial868apg06TXeCo7A1hIQO0ESElHg6D"
	seed, err := r.rt.SecureKeyLoadToken(libraryToken)
	if err != nil {
		t.Fatal(err)
	}
	r.skPtr = seed
	for i := 0; i < 2; i++ {
		if _, err := r.GenerateE2EESecret(); err != nil {
			t.Fatal("email key constructor could not reuse the runner seed")
		}
		if err := r.ClearLoginKey(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.rt.SecureKeyDestroy(seed); err != nil {
		t.Fatal(err)
	}
}
