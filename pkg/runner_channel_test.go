package line

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestChannelCreateWithWASMOnlyKeyDoesNotWriteStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping runner integration test in short mode")
	}

	r, err := GetRunner()
	if err != nil {
		t.Fatalf("GetRunner failed: %v", err)
	}

	keyID, err := r.KeyGenerate()
	if err != nil {
		t.Fatalf("KeyGenerate failed: %v", err)
	}
	peerKeyID, err := r.KeyGenerate()
	if err != nil {
		t.Fatalf("peer KeyGenerate failed: %v", err)
	}
	peerPub, err := r.KeyGetPublic(peerKeyID)
	if err != nil {
		t.Fatalf("KeyGetPublic failed: %v", err)
	}

	var channelID int
	stdout := captureStdout(t, func() {
		channelID, err = r.ChannelCreate(keyID, peerPub)
	})
	if err != nil {
		t.Fatalf("ChannelCreate failed: %v", err)
	}
	if channelID == 0 {
		t.Fatal("ChannelCreate returned empty channel ID")
	}
	if stdout != "" {
		t.Fatalf("ChannelCreate wrote to stdout: %q", stdout)
	}
	if err := r.ReleaseChannel(channelID); err != nil {
		t.Fatalf("ReleaseChannel failed: %v", err)
	}
	if err := r.ReleaseGroupKey(keyID); err != nil {
		t.Fatalf("ReleaseGroupKey(keyID) failed: %v", err)
	}
	if err := r.ReleaseGroupKey(peerKeyID); err != nil {
		t.Fatalf("ReleaseGroupKey(peerKeyID) failed: %v", err)
	}
	// Idempotent release of already-released IDs.
	if err := r.ReleaseGroupKey(keyID); err != nil {
		t.Fatalf("idempotent ReleaseGroupKey failed: %v", err)
	}
	if err := r.ReleaseChannel(channelID); err != nil {
		t.Fatalf("idempotent ReleaseChannel failed: %v", err)
	}
}

func TestSigningRuntimeIsolatedFromMessagingHandles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping runner integration test in short mode")
	}

	r, err := GetRunner()
	if err != nil {
		t.Fatalf("GetRunner failed: %v", err)
	}

	keyID, err := r.KeyGenerate()
	if err != nil {
		t.Fatalf("KeyGenerate failed: %v", err)
	}
	defer func() { _ = r.ReleaseGroupKey(keyID) }()

	epochBefore := r.ResetEpoch()
	sig1, err := r.GetSignature("/api/talk/thrift/Talk/TalkService/getProfile", "[]", "synthetic-access-token")
	if err != nil || sig1 == "" {
		t.Fatalf("GetSignature failed: sig=%q err=%v", sig1, err)
	}

	// Resetting the dedicated signing runtime must not bump the messaging epoch
	// or invalidate E2EE handles.
	r.signingMu.Lock()
	resetErr := r.resetSigningRuntimeLocked()
	r.signingMu.Unlock()
	if resetErr != nil {
		t.Fatalf("resetSigningRuntimeLocked failed: %v", resetErr)
	}
	if r.ResetEpoch() != epochBefore {
		t.Fatalf("signing reset bumped messaging ResetEpoch: %d -> %d", epochBefore, r.ResetEpoch())
	}
	if pub, err := r.KeyGetPublic(keyID); err != nil || pub == "" {
		t.Fatalf("messaging key handle broken by signing reset: pub=%q err=%v", pub, err)
	}

	sig2, err := r.GetSignature("/api/talk/thrift/Talk/TalkService/getProfile", "[]", "synthetic-access-token")
	if err != nil || sig2 != sig1 {
		t.Fatalf("GetSignature after signing reset mismatch: got=%q want=%q err=%v", sig2, sig1, err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&buf, readPipe)
		done <- copyErr
	}()

	os.Stdout = writePipe
	defer func() {
		os.Stdout = oldStdout
	}()
	fn()
	os.Stdout = oldStdout

	if err := writePipe.Close(); err != nil {
		t.Fatalf("closing stdout pipe writer failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("reading captured stdout failed: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("closing stdout pipe reader failed: %v", err)
	}

	return buf.String()
}
