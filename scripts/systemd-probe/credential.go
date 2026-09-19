package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// Format provenance: systemd v257 src/shared/creds-util.{h,c} (LGPL-2.1-or-later).
// These wire identifiers and layouts inform this independent probe parser.
// It performs no cryptography and MUST NOT be treated as authentication.
const (
	hostUserID = "55b9ed1d38594d43a8319d2ebb332ac6"
	tpmUserID  = "ef4ac13679a9480ea7db68897f9f165d"
	pkUserID   = "adbc4ca3efb64201ba881b6f2e4095ea"
	maxBlob    = 128 * 1024
	maxEncoded = 192 * 1024
	maxField   = 16 * 1024
)

var errCredential = errors.New("unsupported or malformed credential envelope")

type protection struct {
	Scheme    string `json:"scheme"`
	PCRMask   uint64 `json:"pcr_mask"`
	PCRBank   uint16 `json:"pcr_bank"`
	SignedPCR uint64 `json:"signed_pcr_mask"`
}

// inspectCredential returns UNVERIFIED header metadata. Only a successful helper
// decrypt of these exact bytes, followed by exact key comparison, verifies it.
// Limits deliberately cover small wrapping credentials, not every systemd format.
func inspectCredential(encoded []byte) (protection, error) {
	var result protection
	if len(encoded) == 0 || len(encoded) > maxEncoded {
		return result, errCredential
	}
	compact := make([]byte, 0, len(encoded))
	for _, b := range encoded {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			compact = append(compact, b)
		}
	}
	defer clear(compact)
	data := make([]byte, base64.StdEncoding.DecodedLen(len(compact)))
	defer clear(data)
	n, err := base64.StdEncoding.Strict().Decode(data, compact)
	if err != nil || n < 32 || n > maxBlob {
		return result, errCredential
	}
	data = data[:n]
	switch hex.EncodeToString(data[:16]) {
	case hostUserID:
		result.Scheme = "host-user"
	case tpmUserID:
		result.Scheme = "host-tpm2-user"
	case pkUserID:
		result.Scheme = "host-tpm2-signed-user"
	default:
		// Includes all system-scoped, TPM-only, null, and unknown identifiers.
		return protection{}, errCredential
	}
	u32 := binary.LittleEndian.Uint32
	u64 := binary.LittleEndian.Uint64
	// AES-256-GCM as emitted by the inspected OpenSSL implementation.
	if u32(data[16:20]) != 32 || u32(data[20:24]) != 1 ||
		u32(data[24:28]) != 12 || u32(data[28:32]) != 16 {
		return protection{}, errCredential
	}
	offset, ok := paddedEnd(data, 0, 44)
	if !ok {
		return protection{}, errCredential
	}
	if result.Scheme != "host-user" {
		if len(data)-offset < 20 {
			return protection{}, errCredential
		}
		h := data[offset:]
		result.PCRMask = u64(h[:8])
		result.PCRBank = binary.LittleEndian.Uint16(h[8:10])
		primary := binary.LittleEndian.Uint16(h[10:12])
		blob, policy := u32(h[12:16]), u32(h[16:20])
		if result.PCRMask >= 1<<24 || (result.PCRBank != 4 && result.PCRBank != 11) ||
			(primary != 1 && primary != 0x23) || blob == 0 || blob > maxField || policy == 0 || policy > maxField {
			return protection{}, errCredential
		}
		offset, ok = paddedEnd(data, offset, 20+int(blob)+int(policy))
		if !ok {
			return protection{}, errCredential
		}
		if result.Scheme == "host-tpm2-signed-user" {
			if len(data)-offset < 12 {
				return protection{}, errCredential
			}
			result.SignedPCR = u64(data[offset : offset+8])
			size := u32(data[offset+8 : offset+12])
			if result.SignedPCR == 0 || result.SignedPCR >= 1<<24 || size == 0 || size > maxField {
				return protection{}, errCredential
			}
			offset, ok = paddedEnd(data, offset, 12+int(size))
			if !ok {
				return protection{}, errCredential
			}
		}
	}
	// User-scoping flags bind UID, machine ID and username. Then require space
	// for encrypted metadata (at least 24 bytes), a 32-byte key and the GCM tag.
	if len(data)-offset < 8+24+32+16 || u64(data[offset:offset+8]) != 7 {
		return protection{}, errCredential
	}
	return result, nil
}

func paddedEnd(data []byte, start, size int) (int, bool) {
	if start < 0 || start > len(data) || size < 0 || size > len(data)-start {
		return 0, false
	}
	end := start + size
	aligned := (end + 7) &^ 7
	if aligned > len(data) || !bytes.Equal(data[end:aligned], make([]byte, aligned-end)) {
		return 0, false
	}
	return aligned, true
}
