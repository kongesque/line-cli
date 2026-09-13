package session

import (
	"errors"
	"fmt"
	"testing"
)

func TestStorageExitCodesPreserveErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
		code   int
	}{
		{nil, "ok", 0}, {ErrBusy, "busy", 75}, {ErrStorageChanged, "storage_changed", 75},
		{ErrHostConsent, "host_consent_required", 78}, {ErrMigrationRequired, "migration_required", 78}, {ErrCleanupPending, "cleanup_pending", 78},
		{ErrStorageFormat, "invalid_format", 65}, {ErrStorageAuthentication, "authentication_failed", 65}, {errMissingWrappingKey, "missing_key", 65},
		{ErrCredentialHelper, "storage_unavailable", 69}, {ErrCredentialTimeout, "helper_timeout", 69}, {ErrHeadlessUnavailable, "headless_unavailable", 69},
		{ErrDurabilityUncertain, "durability_uncertain", 74}, {ErrStorageCleanup, "probe_cleanup_failed", 74},
		{errors.New("unrelated network error"), "storage_error", 1},
	} {
		err := tc.err
		if err != nil {
			err = fmt.Errorf("context: %w", err)
		}
		if StorageReason(err) != tc.reason || StorageExitCode(err) != tc.code {
			t.Fatal("unexpected error classification", tc.reason)
		}
	}
}
