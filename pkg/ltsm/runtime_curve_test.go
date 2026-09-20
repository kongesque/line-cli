package ltsm

import (
	"bytes"
	"testing"
)

func TestCurve25519KeyGenerateAndDestroy(t *testing.T) {
	rt, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err := rt.Curve25519KeyDestroy(0); err != nil {
		t.Fatal(err)
	}
	var previous []byte
	for i := 0; i < 8; i++ {
		key, err := rt.Curve25519KeyGenerate()
		if err != nil {
			t.Fatal(err)
		}
		pub, err := rt.Curve25519KeyGetPublicKey(key)
		if err != nil || len(pub) != 32 || bytes.Equal(pub, previous) {
			t.Fatal("generation did not produce a fresh public key")
		}
		previous = bytes.Clone(pub)
		if err := rt.Curve25519KeyDestroy(key); err != nil {
			t.Fatal(err)
		}
	}
}
