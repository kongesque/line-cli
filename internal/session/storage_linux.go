package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// Resolve the bytes on every locked operation. Never cache a selected backend
// or wrapping key across watch iterations, token updates, or migrations.
type linuxStorage struct {
	native   secretFileStore
	provider func(context.Context) (sealedKeyProvider, error)
	context  context.Context
}

func resolvedLinuxStorage() (linuxStorage, error) {
	native, err := linuxStore()
	return linuxStorage{native: native, provider: newSystemdCredentials}, err
}

func (s linuxStorage) ctx() context.Context {
	if s.context != nil {
		return s.context
	}
	return context.Background()
}

func (s linuxStorage) read() ([]byte, error) {
	files, err := s.native.files(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer files.root.Close()
	data, err := files.read(filepath.Base(s.native.path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

func (s linuxStorage) openEnvelope(data []byte) (headlessEnvelope, []byte, *State, error) {
	e, err := parseEnvelope(data)
	if err != nil {
		return e, nil, nil, err
	}
	provider, err := s.provider(s.ctx())
	if err != nil {
		return e, nil, nil, err
	}
	key, err := provider.Unseal(s.ctx(), e.sealed, e.protection)
	if err != nil {
		clear(key)
		return e, nil, nil, err
	}
	state, err := e.open(key)
	if err != nil {
		clear(key)
		return e, nil, nil, err
	}
	return e, key, state, nil
}

func (s linuxStorage) Load() (*State, error) {
	data, err := s.read()
	if err != nil {
		return nil, err
	}
	if !hasEnvelopeMarker(data) {
		return s.native.decode(data)
	}
	_, key, state, err := s.openEnvelope(data)
	clear(key)
	return state, err
}

func (s linuxStorage) Save(state *State) error {
	data, err := s.read()
	if errors.Is(err, ErrNotFound) {
		return s.native.Save(state)
	}
	if err != nil {
		return err
	}
	if !hasEnvelopeMarker(data) {
		// A damaged new-format marker must not let Save overwrite the file as
		// native. Existing legacy ciphertext must authenticate first.
		if _, err := s.native.decode(data); err != nil {
			return err
		}
		return s.native.Save(state)
	}
	e, key, _, err := s.openEnvelope(data)
	if err != nil {
		return err
	}
	defer clear(key)
	updated, err := encodeEnvelope(state, e.sealed, key, e.protection)
	if err != nil {
		return err
	}
	files, err := s.native.files(false)
	if err != nil {
		return err
	}
	defer files.root.Close()
	return files.replace(filepath.Base(s.native.path), updated)
}

func (s linuxStorage) Prepare() (result error) {
	data, err := s.read()
	if errors.Is(err, ErrNotFound) {
		return prepareSecretFileStore(s.native)
	}
	if err != nil {
		return err
	}
	if !hasEnvelopeMarker(data) {
		return prepareSecretFileStore(s.native)
	}
	e, key, _, err := s.openEnvelope(data)
	if err != nil {
		return err
	}
	defer clear(key)
	files, err := s.native.files(false)
	if err != nil {
		return err
	}
	defer files.root.Close()
	name := ".storage-probe-" + rand.Text()
	defer func() {
		if err := files.remove(name); err != nil {
			result = errors.Join(result, ErrStorageCleanup)
		}
	}()
	probe := &State{Version: 1, MID: "storage-probe", AccessToken: rand.Text(), Generation: rand.Text()}
	for i := 0; i < 2; i++ {
		probe.LastReqSeq = int64(i)
		encoded, err := encodeEnvelope(probe, e.sealed, key, e.protection)
		if err != nil {
			return err
		}
		if err := files.replace(name, encoded); err != nil {
			return err
		}
		saved, err := files.read(name)
		if err != nil {
			return err
		}
		parsed, err := parseEnvelope(saved)
		if err != nil {
			return err
		}
		got, err := parsed.open(key)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(probe, got) {
			return ErrStorageAuthentication
		}
	}
	return nil
}

// Produces a verified candidate only; the caller owns explicit protection
// acceptance and the locked enrollment/migration transaction. No disk write.
func createHeadlessSession(ctx context.Context, state *State, provider sealedKeyProvider, required protectionRequirement) ([]byte, error) {
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	blob, policy, err := provider.Seal(ctx, key, required)
	if err != nil {
		return nil, err
	}
	defer clear(blob)
	if (required == requireHost && policy.Scheme != "host-user") ||
		(required == requireTPM && policy.Scheme != "host-tpm2-user") ||
		(required != requireHost && required != requireTPM) {
		return nil, ErrCredentialPolicy
	}
	verified, err := provider.Unseal(ctx, blob, policy)
	defer clear(verified)
	if err != nil {
		return nil, err
	}
	if len(verified) != 32 || subtle.ConstantTimeCompare(key, verified) != 1 {
		return nil, ErrStorageAuthentication
	}
	data, err := encodeEnvelope(state, blob, key, policy)
	if err != nil {
		return nil, err
	}
	e, err := parseEnvelope(data)
	if err != nil {
		return nil, err
	}
	got, err := e.open(verified)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(state, got) {
		return nil, ErrStorageAuthentication
	}
	return data, nil
}
