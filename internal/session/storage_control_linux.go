package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path/filepath"
)

func platformStorageIdentity() (string, error) {
	s, err := resolvedLinuxStorage()
	if err != nil {
		return "", err
	}
	return s.StorageIdentity()
}
func (s linuxStorage) StorageIdentity() (string, error) {
	if err := s.checkCleanup(); err != nil {
		return "", err
	}
	data, err := s.read()
	if errors.Is(err, ErrNotFound) {
		return "none", nil
	}
	if err != nil {
		return "", err
	}
	if !hasEnvelopeMarker(data) {
		return "native", nil
	}
	e, err := parseEnvelope(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(e.sealed)
	return "headless:" + hex.EncodeToString(sum[:]), nil
}

type headlessLogin struct {
	base                      linuxStorage
	candidate                 []byte
	expected, mid, generation string
	accepted                  bool
}

func beginHeadlessLogin(ctx context.Context) (LoginStorage, error) {
	s, err := resolvedLinuxStorage()
	if err != nil {
		return nil, err
	}
	s.context = ctx
	return s.beginLogin()
}
func (s linuxStorage) beginLogin() (LoginStorage, error) {
	identity, err := s.StorageIdentity()
	if err != nil {
		return nil, err
	}
	if identity == "native" {
		return nil, ErrMigrationRequired
	}
	p := &headlessLogin{base: s, expected: identity}
	if identity == "none" {
		provider, err := s.provider(s.ctx())
		if err != nil {
			return nil, err
		}
		// The initial CLI release enrolls explicit host-only protection. It
		// never infers a working physical TPM from automatic helper selection.
		candidate, err := createHeadlessSession(s.ctx(), &State{Version: 1, MID: "storage-probe", AccessToken: "synthetic-probe"}, provider, requireHost)
		if err != nil {
			return nil, err
		}
		p.candidate = candidate
		if err := s.probeEnvelope(candidate); err != nil {
			p.Close()
			return nil, err
		}
	} else {
		state, err := s.Load()
		if err != nil {
			return nil, err
		}
		p.mid, p.generation = state.MID, state.Generation
		p.accepted = true
		if err := s.Prepare(); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *headlessLogin) RequiresHostConsent() bool        { return len(p.candidate) > 0 }
func (p *headlessLogin) AcceptHost()                      { p.accepted = true }
func (p *headlessLogin) Close()                           { clear(p.candidate); p.candidate = nil; p.accepted = false }
func (p *headlessLogin) Load() (*State, error)            { return p.base.Load() }
func (p *headlessLogin) Delete() error                    { return p.base.Delete() }
func (p *headlessLogin) StorageIdentity() (string, error) { return p.base.StorageIdentity() }
func (p *headlessLogin) checkExpected() error {
	identity, err := p.base.StorageIdentity()
	if err != nil {
		return err
	}
	if identity != p.expected {
		return ErrStorageChanged
	}
	if identity != "none" {
		state, err := p.base.Load()
		if err != nil {
			return err
		}
		if state.MID != p.mid || state.Generation != p.generation {
			return ErrStorageChanged
		}
	}
	return nil
}
func (p *headlessLogin) Prepare() error {
	if !p.accepted {
		return ErrHostConsent
	}
	if err := p.checkExpected(); err != nil {
		return err
	}
	if len(p.candidate) > 0 {
		return p.base.probeEnvelope(p.candidate)
	}
	return p.base.Prepare()
}
func (p *headlessLogin) Save(state *State) error {
	if !p.accepted {
		return ErrHostConsent
	}
	if err := p.checkExpected(); err != nil {
		return err
	}
	if len(p.candidate) == 0 {
		return p.base.Save(state)
	}
	e, key, _, err := p.base.openEnvelope(p.candidate)
	if err != nil {
		return err
	}
	defer clear(key)
	data, err := encodeEnvelope(state, e.sealed, key, e.protection)
	if err != nil {
		return err
	}
	files, err := p.base.native.files(true)
	if err != nil {
		return err
	}
	defer files.root.Close()
	return files.replace(filepath.Base(p.base.native.path), data)
}

func platformStorageStatus(check bool) (StorageStatus, error) {
	s, err := resolvedLinuxStorage()
	if err != nil {
		return initialStorageStatus("unknown"), err
	}
	return s.Status(check)
}
func (s linuxStorage) Status(check bool) (status StorageStatus, result error) {
	status = initialStorageStatus("unknown")
	defer func() {
		if result != nil {
			status.Reason = StorageReason(result)
		}
	}()
	if err := s.checkCleanup(); err != nil {
		if errors.Is(err, ErrCleanupPending) {
			status.Configured = "cleanup_pending"
		}
		if errors.Is(err, ErrMigrationPending) {
			status.Configured = "present"
			status.Backend = "native"
			status.WriteAccess = "unavailable"
		}
		return status, err
	}
	data, err := s.read()
	if errors.Is(err, ErrNotFound) {
		status.Configured = "none"
		status.Backend = "native"
		status.ReadAccess = "not_applicable"
		status.Reason = "no_session"
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Configured = "present"
	var state *State
	if hasEnvelopeMarker(data) {
		status.Backend = "headless"
		e, err := parseEnvelope(data)
		if err != nil {
			return status, err
		}
		status.Protection = e.protection.Scheme
		status.FixedPCR = e.protection.PCRMask
		status.PCRBank = e.protection.PCRBank
		status.SignedPCR = e.protection.SignedPCR
		_, key, loaded, err := s.openEnvelope(data)
		clear(key)
		if err != nil {
			status.ReadAccess = "unavailable"
			return status, err
		}
		state = loaded
		status.Reboot = "expected_not_verified"
	} else {
		status.Backend = "native"
		status.Protection = "secret_service"
		key, err := s.native.secrets.loadKeyWithoutUnlock()
		if err != nil {
			status.ReadAccess = "unavailable"
			return status, err
		}
		defer clear(key)
		plain, err := decryptSession(data, key)
		if err != nil {
			return status, err
		}
		defer clear(plain)
		state, err = decodeSession(plain)
		if err != nil {
			return status, errors.Join(ErrStorageFormat, err)
		}
	}
	status.ReadAccess = "available"
	status.ProtectionVerified = true
	status.Email = state.Email
	if check {
		if status.Backend == "native" {
			status.WriteAccess = "unknown"
			return status, ErrReadOnlyStatus
		}
		if err := s.Prepare(); err != nil {
			status.WriteAccess = "unavailable"
			return status, err
		}
		status.WriteAccess = "available"
	}
	files, err := s.native.files(false)
	if err != nil {
		return status, err
	}
	defer files.root.Close()
	if r, err := readMigration(files); err != nil {
		return status, err
	} else if r != nil {
		return status, ErrMigrationPending
	}
	return status, nil
}

// libsecret search uses LOAD_SECRETS without UNLOCK unless --unlock is given.
// Parse only the exact key line and never relay the listing or diagnostics.
// Provenance: GNOME libsecret tool/secret-tool.c, search flags and output format.
func (s secretToolStore) loadKeyWithoutUnlock() ([]byte, error) {
	account := s.account
	if account == "" {
		account = "default"
	}
	r := s.run([]string{"search", "--all", "service", "io.github.kongesque.line-cli.encryption", "account", account}, nil)
	defer clear(r.output)
	if r.err != nil || r.code != 0 {
		return nil, ErrStorageUnavailable
	}
	var encoded []byte
	count := 0
	items := 0
	for _, line := range bytes.Split(r.output, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte{'['}) {
			items++
		}
		if bytes.HasPrefix(line, []byte("secret = ")) {
			count++
			encoded = line[len("secret = "):]
		}
	}
	if count != 1 || items != 1 {
		return nil, ErrStorageUnavailable
	}
	key, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, errMissingWrappingKey
	}
	return key, nil
}

func (s secretToolStore) hasKeyWithoutUnlock() (bool, error) {
	account := s.account
	if account == "" {
		account = "default"
	}
	r := s.run([]string{"search", "--all", "service", "io.github.kongesque.line-cli.encryption", "account", account}, nil)
	defer clear(r.output)
	if r.err != nil || r.code != 0 {
		return false, ErrStorageUnavailable
	}
	if len(bytes.TrimSpace(r.output)) == 0 && !r.diagnostic {
		return false, nil
	}
	for _, line := range bytes.Split(r.output, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte{'['}) {
			return true, nil
		}
	}
	return false, ErrStorageUnavailable
}
