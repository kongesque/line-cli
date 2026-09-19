package session

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"slices"
	"testing"
)

// Fixtures describe header layouts only; they are not authentic systemd blobs.
func fixture(id string) []byte {
	b := make([]byte, 48)
	idBytes, _ := hex.DecodeString(id)
	copy(b, idBytes)
	for i, v := range []uint32{32, 1, 12, 16} {
		binary.LittleEndian.PutUint32(b[16+i*4:], v)
	}
	if id == tpmUserID || id == pkUserID {
		h := make([]byte, 56)
		binary.LittleEndian.PutUint64(h, 1<<7)
		binary.LittleEndian.PutUint16(h[8:], 11)
		binary.LittleEndian.PutUint16(h[10:], 0x23)
		binary.LittleEndian.PutUint32(h[12:], 4)
		binary.LittleEndian.PutUint32(h[16:], 32)
		b = append(b, h...)
		if id == pkUserID {
			h := make([]byte, 16)
			binary.LittleEndian.PutUint64(h, 1<<11)
			binary.LittleEndian.PutUint32(h[8:], 4)
			b = append(b, h...)
		}
	}
	h := make([]byte, 8+24+32+16)
	binary.LittleEndian.PutUint64(h, 7)
	return append(b, h...)
}

func encode(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

func TestCredentialLayouts(t *testing.T) {
	for _, tc := range []struct {
		id, scheme  string
		pcr, signed uint64
	}{
		{hostUserID, "host-user", 0, 0},
		{tpmUserID, "host-tpm2-user", 1 << 7, 0},
		{pkUserID, "host-tpm2-signed-user", 1 << 7, 1 << 11},
	} {
		t.Run(tc.scheme, func(t *testing.T) {
			got, err := inspectCredential(append(encode(fixture(tc.id)), '\n'))
			if err != nil || got.Scheme != tc.scheme || got.PCRMask != tc.pcr || got.SignedPCR != tc.signed {
				t.Fatal("layout classification failed")
			}
		})
	}
}

func TestCredentialRejectsEveryTruncation(t *testing.T) {
	for _, id := range []string{hostUserID, tpmUserID, pkUserID} {
		b := fixture(id)
		for n := range b {
			if _, err := inspectCredential(encode(b[:n])); err == nil {
				t.Fatalf("accepted truncated fixture at %d", n)
			}
		}
	}
}

func TestCredentialRejectsUnsupportedSchemesAndFields(t *testing.T) {
	for _, id := range []string{
		"058469daf6f54324800549da0f8ea2fb", // null
		"5a1c6a86df9d4096b1d5a65e0862f19a", // system host
		"0c7cc07b117645919c4b0bea08bc20fe", // TPM-only
		"00000000000000000000000000000000",
	} {
		if _, err := inspectCredential(encode(fixture(id))); err == nil {
			t.Fatal("accepted forbidden identifier")
		}
	}
	for _, tc := range []struct {
		name   string
		id     string
		mutate func([]byte)
	}{
		{"key size", hostUserID, func(b []byte) { b[16] = 16 }},
		{"block size", hostUserID, func(b []byte) { b[20] = 16 }},
		{"iv size", hostUserID, func(b []byte) { b[24] = 0 }},
		{"tag size", hostUserID, func(b []byte) { b[28] = 0 }},
		{"padding", hostUserID, func(b []byte) { b[44] = 1 }},
		{"scope flags", hostUserID, func(b []byte) { b[48] = 0 }},
		{"PCR mask", tpmUserID, func(b []byte) { b[51] = 1 }},
		{"PCR bank", tpmUserID, func(b []byte) { b[56] = 0 }},
		{"primary algorithm", tpmUserID, func(b []byte) { b[58] = 0 }},
		{"blob bounds", tpmUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[60:], ^uint32(0)) }},
		{"policy bounds", tpmUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[64:], ^uint32(0)) }},
		{"signed PCR mask", pkUserID, func(b []byte) { clear(b[104:112]) }},
		{"public key bounds", pkUserID, func(b []byte) { binary.LittleEndian.PutUint32(b[112:], ^uint32(0)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixture(tc.id)
			tc.mutate(b)
			if _, err := inspectCredential(encode(b)); err == nil {
				t.Fatal("accepted invalid field")
			}
		})
	}
	for _, b := range [][]byte{nil, []byte("invalid-base64!"), bytes.Repeat([]byte{'A'}, maxEncoded+1), encode(make([]byte, maxBlob+1))} {
		if _, err := inspectCredential(b); err == nil {
			t.Fatal("accepted invalid encoding/size")
		}
	}
}

func FuzzCredential(f *testing.F) {
	for _, id := range []string{hostUserID, tpmUserID, pkUserID} {
		f.Add(encode(fixture(id)))
	}
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := inspectCredential(b)
		if err == nil && !slices.Contains([]string{"host-user", "host-tpm2-user", "host-tpm2-signed-user"}, p.Scheme) {
			t.Fatal("unexpected scheme")
		}
	})
}
