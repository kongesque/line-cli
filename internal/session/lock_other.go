//go:build !darwin

package session

func Lock() (func(), error) { return nil, errKeychainUnsupported }
