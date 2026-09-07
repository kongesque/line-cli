package session

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("no saved LINE session; run line login")

// State is secret material. Never print it or include it in diagnostic errors.
// Passwords are deliberately absent: expired refresh credentials require login.
type State struct {
	Version      int               `json:"version"`
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	Certificate  string            `json:"certificate,omitempty"`
	MID          string            `json:"mid"`
	Email        string            `json:"email"`
	NoE2EE       bool              `json:"no_e2ee"`
	ExportedKeys map[string]string `json:"exported_keys,omitempty"`
	RefreshAt    time.Time         `json:"refresh_at,omitempty"`
	Invalidated  bool              `json:"invalidated,omitempty"`
}

type Store interface {
	Load() (*State, error)
	Save(*State) error
	Delete() error
}

type KeychainStore struct{}
