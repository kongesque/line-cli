//go:build !darwin || !cgo

package session

import "errors"

var errKeychainUnsupported = errors.New("this version requires macOS with a CGO-enabled build for Keychain storage")

func (KeychainStore) Load() (*State, error) { return nil, errKeychainUnsupported }
func (KeychainStore) Save(*State) error     { return errKeychainUnsupported }
func (KeychainStore) Delete() error         { return errKeychainUnsupported }
