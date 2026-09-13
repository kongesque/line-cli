package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// Preserve the legacy native file format; key acquisition belongs to the provider.
func sessionCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid session encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptSession(plain, key []byte) ([]byte, error) {
	if len(plain) > maxSessionBytes-1024 {
		return nil, errors.New("could not encode credential session")
	}
	aead, err := sessionCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte("line-cli-session-v1")), nil
}

func decryptSession(data, key []byte) ([]byte, error) {
	aead, err := sessionCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize()+aead.Overhead() || len(data) > maxSessionBytes {
		return nil, errors.New("invalid encrypted session")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("line-cli-session-v1"))
	if err != nil {
		return nil, errors.New("could not authenticate encrypted session; restore storage or run line logout before signing in again")
	}
	return plain, nil
}
