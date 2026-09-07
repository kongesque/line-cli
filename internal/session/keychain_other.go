//go:build (darwin && !cgo) || (!darwin && !linux && !windows)

package session

import "errors"

var errKeychainUnsupported = errors.New("credential storage requires macOS with CGO, Linux Secret Service, or Windows DPAPI")

func (KeychainStore) Load() (*State, error) { return nil, errKeychainUnsupported }
func (KeychainStore) Save(*State) error     { return errKeychainUnsupported }
func (KeychainStore) Delete() error         { return errKeychainUnsupported }
