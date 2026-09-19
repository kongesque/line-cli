package session

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
)

// Version and backend are independent of the encrypted State.Version. All bytes
// preceding the ciphertext, including the sealed credential, are authenticated.
const (
	envelopeMagic      = "LINECLI\x00SESSION\x00"
	envelopeVersion    = 1
	backendSystemd     = 1
	envelopeFixedBytes = 52
)

var (
	ErrStorageFormat         = errors.New("unsupported or malformed session storage format")
	ErrStorageAuthentication = errors.New("could not authenticate saved session storage")
)

// Metadata is unverified until both systemd unsealing and session AEAD succeed.
type headlessEnvelope struct {
	header, sealed, nonce, ciphertext []byte
	protection                        credentialProtection
}

func hasEnvelopeMarker(data []byte) bool {
	return bytes.HasPrefix(data, []byte(envelopeMagic))
}

func parseEnvelope(data []byte) (headlessEnvelope, error) {
	var e headlessEnvelope
	if len(data) < envelopeFixedBytes+16 || len(data) > maxSessionBytes || !hasEnvelopeMarker(data) {
		return e, ErrStorageFormat
	}
	u16, u32 := binary.LittleEndian.Uint16, binary.LittleEndian.Uint32
	if u16(data[16:18]) != envelopeVersion || u16(data[18:20]) != backendSystemd ||
		!bytes.Equal(data[25:28], []byte{0, 0, 0}) || u16(data[34:36]) != 0 {
		return e, ErrStorageFormat
	}
	size := u32(data[20:24])
	if size == 0 || size > maxEncoded || int(size) > len(data)-envelopeFixedBytes-16 {
		return e, ErrStorageFormat
	}
	headerEnd := envelopeFixedBytes + int(size)
	e.header, e.sealed, e.nonce, e.ciphertext = data[:headerEnd], data[envelopeFixedBytes:headerEnd], data[40:52], data[headerEnd:]
	p, err := inspectCredential(e.sealed)
	if err != nil || schemeID(p.Scheme) != data[24] || p.PCRMask != uint64(u32(data[28:32])) ||
		p.PCRBank != u16(data[32:34]) || p.SignedPCR != uint64(u32(data[36:40])) {
		return headlessEnvelope{}, ErrStorageFormat
	}
	e.protection = p
	return e, nil
}

func schemeID(scheme string) byte {
	switch scheme {
	case "host-user":
		return 1
	case "host-tpm2-user":
		return 2
	case "host-tpm2-signed-user":
		return 3
	default:
		return 0
	}
}

func encodeEnvelope(state *State, sealed, key []byte, policy credentialProtection) ([]byte, error) {
	p, err := inspectCredential(sealed)
	if err != nil || p != policy {
		return nil, ErrStorageFormat
	}
	plain, err := json.Marshal(state)
	defer clear(plain)
	if err != nil || len(plain) > maxSessionBytes-envelopeFixedBytes-len(sealed)-16 {
		return nil, ErrStorageFormat
	}
	if _, err := decodeSession(plain); err != nil {
		return nil, err
	}
	aead, err := sessionCipher(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, envelopeFixedBytes+len(sealed))
	copy(header, envelopeMagic)
	binary.LittleEndian.PutUint16(header[16:18], envelopeVersion)
	binary.LittleEndian.PutUint16(header[18:20], backendSystemd)
	binary.LittleEndian.PutUint32(header[20:24], uint32(len(sealed)))
	header[24] = schemeID(policy.Scheme)
	binary.LittleEndian.PutUint32(header[28:32], uint32(policy.PCRMask))
	binary.LittleEndian.PutUint16(header[32:34], policy.PCRBank)
	binary.LittleEndian.PutUint32(header[36:40], uint32(policy.SignedPCR))
	if _, err := rand.Read(header[40:52]); err != nil {
		return nil, err
	}
	copy(header[envelopeFixedBytes:], sealed)
	return aead.Seal(header, header[40:52], plain, header), nil
}

func (e headlessEnvelope) open(key []byte) (*State, error) {
	aead, err := sessionCipher(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, e.nonce, e.ciphertext, e.header)
	if err != nil {
		return nil, ErrStorageAuthentication
	}
	defer clear(plain)
	return decodeSession(plain)
}
