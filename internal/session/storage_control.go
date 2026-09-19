package session

import (
	"context"
	"errors"
	"runtime"
)

var (
	ErrHeadlessUnsupported = errors.New("headless storage is available only on Linux")
	ErrHostConsent         = errors.New("host-only storage requires explicit acceptance")
	ErrMigrationRequired   = errors.New("a native session exists; run line auth migrate --storage=headless")
	ErrMigrationPending    = errors.New("storage migration needs completion; run line auth migrate --storage=headless again")
	ErrNativeKeyShared     = errors.New("native key ownership is shared or unverified; stop other profiles and resolve their native sessions before retrying cleanup")
	ErrStorageChanged      = errors.New("saved storage changed during login; run login again")
	ErrCleanupPending      = errors.New("local logout cleanup is incomplete; run line logout again")
	ErrStorageRepair       = errors.New("storage metadata needs repair; existing data was preserved")
	ErrReadOnlyStatus      = errors.New("this native backend cannot prove write readiness without a possible unlock prompt; use interactive line login")
	ErrStorageUnavailable  = errors.New("native storage is unavailable or locked")
	ErrHeadlessUnavailable = errors.New("user-scoped systemd credential storage is unavailable")
	ErrCredentialHelper    = errors.New("systemd credential operation failed")
	ErrCredentialTimeout   = errors.New("systemd credential operation timed out")
	ErrCredentialCancelled = errors.New("systemd credential operation was cancelled")
	ErrCredentialPolicy    = errors.New("systemd credential protection does not match the required policy")
	ErrCredentialOutput    = errors.New("systemd credential output exceeds the limit")
)

type LoginStorage interface {
	Store
	StoragePreparer
	StorageIdentity() (string, error)
	RequiresHostConsent() bool
	AcceptHost()
	Close()
}

func SupportsHeadless() bool                                       { return runtime.GOOS == "linux" }
func BeginHeadlessLogin(ctx context.Context) (LoginStorage, error) { return beginHeadlessLogin(ctx) }

// MigrateHeadless must be called while holding Lock. It never contacts LINE.
func MigrateHeadless(ctx context.Context, accepted bool) error { return migrateHeadless(ctx, accepted) }
func (KeychainStore) StorageIdentity() (string, error)         { return platformStorageIdentity() }

// Contains only safe local status. Protection is verified only after successful
// decryption. LINEValidity never asserts that local credentials remain valid.
type StorageStatus struct {
	Schema             int    `json:"schema"`
	Configured         string `json:"configured"`
	Backend            string `json:"backend"`
	Email              string `json:"email,omitempty"`
	Protection         string `json:"protection"`
	ProtectionVerified bool   `json:"protection_verified"`
	FixedPCR           uint64 `json:"fixed_pcr_mask,omitempty"`
	PCRBank            uint16 `json:"pcr_bank,omitempty"`
	SignedPCR          uint64 `json:"signed_pcr_mask,omitempty"`
	ReadAccess         string `json:"read_access"`
	WriteAccess        string `json:"write_access"`
	Reboot             string `json:"after_reboot"`
	LINEValidity       string `json:"line_validity"`
	Reason             string `json:"reason"`
}

func initialStorageStatus(backend string) StorageStatus {
	return StorageStatus{Schema: 1, Configured: "unknown", Backend: backend, Protection: "unverified", ReadAccess: "unknown", WriteAccess: "not_checked", Reboot: "not_verified", LINEValidity: "not_checked", Reason: "ok"}
}
func (KeychainStore) Status(check bool) (StorageStatus, error) {
	status, err := platformStorageStatus(check)
	if err != nil {
		status.Reason = StorageReason(err)
	}
	return status, err
}

func StorageReason(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrDurabilityUncertain):
		return "durability_uncertain"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrNotFound):
		return "no_session"
	case errors.Is(err, ErrCleanupPending):
		return "cleanup_pending"
	case errors.Is(err, ErrMigrationPending):
		return "migration_pending"
	case errors.Is(err, ErrNativeKeyShared):
		return "native_key_shared"
	case errors.Is(err, ErrStorageChanged):
		return "storage_changed"
	case errors.Is(err, ErrHostConsent):
		return "host_consent_required"
	case errors.Is(err, ErrMigrationRequired):
		return "migration_required"
	case errors.Is(err, ErrHeadlessUnsupported):
		return "unsupported_platform"
	case errors.Is(err, ErrReadOnlyStatus):
		return "interactive_check_required"
	case errors.Is(err, ErrUnsafeStorage):
		return "unsafe_storage"
	case errors.Is(err, ErrStorageRepair):
		return "repair_required"
	case errors.Is(err, ErrStorageCleanup):
		return "probe_cleanup_failed"
	case errors.Is(err, ErrCredentialCancelled):
		return "cancelled"
	case errors.Is(err, ErrCredentialTimeout):
		return "helper_timeout"
	case errors.Is(err, ErrCredentialOutput):
		return "helper_output_limit"
	case errors.Is(err, ErrCredentialPolicy):
		return "protection_mismatch"
	case errors.Is(err, ErrStorageAuthentication):
		return "authentication_failed"
	case errors.Is(err, ErrStorageFormat):
		return "invalid_format"
	case errors.Is(err, errMissingWrappingKey):
		return "missing_key"
	case errors.Is(err, ErrHeadlessUnavailable):
		return "headless_unavailable"
	case errors.Is(err, ErrCredentialHelper), errors.Is(err, ErrStorageUnavailable):
		return "storage_unavailable"
	default:
		return "storage_error"
	}
}

// Only classified storage errors get dedicated codes; unrelated CLI/network
// errors retain the existing generic status 1.
func StorageExitCode(err error) int {
	switch StorageReason(err) {
	case "ok":
		return 0
	case "busy", "storage_changed", "cancelled":
		return 75
	case "durability_uncertain", "probe_cleanup_failed":
		return 74
	case "storage_unavailable", "helper_timeout", "headless_unavailable":
		return 69
	case "authentication_failed", "invalid_format", "missing_key", "protection_mismatch", "helper_output_limit":
		return 65
	case "cleanup_pending", "migration_pending", "native_key_shared", "host_consent_required", "migration_required", "unsupported_platform", "interactive_check_required", "unsafe_storage", "repair_required":
		return 78
	default:
		return 1
	}
}
