package session

import (
	"errors"
	"time"

	"github.com/kongesque/line-cli/pkg/line"
)

var ErrNotFound = errors.New("no saved LINE session; run line login")

// Only an explicit server logout invalidates credentials; token age never does.
var ErrSessionInvalidated = errors.New("LINE session was invalidated or replaced by another client; run line login")
var ErrSessionChanged = errors.New("LINE login changed; restart the command")

// ErrWatchBusy means another line watch holds the watcher lock.
var ErrWatchBusy = errors.New("another line watch is already running")

// State is secret material. Never print it or include it in diagnostic errors.
// Passwords are deliberately absent: expired refresh credentials require login.
type State struct {
	DurationUntilRefreshSec string                      `json:"duration_until_refresh_sec,omitempty"`
	TokenIssueTimeEpochSec  string                      `json:"token_issue_time_epoch_sec,omitempty"`
	RefreshApiRetryPolicy   *line.RefreshApiRetryPolicy `json:"refresh_api_retry_policy,omitempty"`

	Generation        string            `json:"generation,omitempty"`
	WatchRevision     *int64            `json:"watch_revision,omitempty"`
	Version           int               `json:"version"`
	AccessToken       string            `json:"access_token"`
	RefreshToken      string            `json:"refresh_token,omitempty"`
	CertificateOrigin string            `json:"certificate_origin,omitempty"` // Empty means unknown (legacy).
	Certificate       string            `json:"certificate,omitempty"`
	MID               string            `json:"mid"`
	Email             string            `json:"email"`
	NoE2EE            bool              `json:"no_e2ee"`
	ExportedKeys      map[string]string `json:"exported_keys,omitempty"`
	RefreshAt         time.Time         `json:"refresh_at,omitempty"`
	Invalidated       bool              `json:"invalidated,omitempty"`
	LastReqSeq        int64             `json:"last_req_seq,omitempty"`
}

type Store interface {
	Load() (*State, error)
	Save(*State) error
	Delete() error
}

type KeychainStore struct{}
