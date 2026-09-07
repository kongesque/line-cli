//go:build !darwin

package session

import "errors"

var ErrBusy = errors.New("session is busy")

func WatchLock() (func(), error) { return nil, errKeychainUnsupported }

func Lock() (func(), error) { return nil, errKeychainUnsupported }
