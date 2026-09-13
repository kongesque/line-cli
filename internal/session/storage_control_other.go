//go:build !linux

package session

import (
	"context"
	"errors"
)

func beginHeadlessLogin(context.Context) (LoginStorage, error) { return nil, ErrHeadlessUnsupported }
func platformStorageIdentity() (string, error)                 { return "native", nil }

func nativeStorageStatus(store Store, check bool, prepare func() error) (StorageStatus, error) {
	status := initialStorageStatus("native")
	state, err := store.Load()
	if errors.Is(err, ErrNotFound) {
		status.Configured = "none"
		status.ReadAccess = "not_applicable"
		status.Reason = "no_session"
		return status, nil
	}
	if err != nil {
		status.ReadAccess = "unavailable"
		status.Reason = StorageReason(err)
		return status, err
	}
	if state == nil {
		status.Reason = "invalid_format"
		return status, ErrStorageFormat
	}
	status.Configured = "present"
	status.ReadAccess = "available"
	status.Protection = "native_os"
	status.ProtectionVerified = true
	status.Email = state.Email
	if check {
		err = prepare()
		if err == nil {
			status.WriteAccess = "available"
		} else {
			status.WriteAccess = "unknown"
			status.Reason = StorageReason(err)
		}
	}
	return status, err
}
