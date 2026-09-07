package session

import (
	"os"
	"path/filepath"
)

func linuxStore() (secretFileStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return secretFileStore{}, err
	}
	return secretFileStore{path: filepath.Join(dir, "line-cli", "session.enc"), secrets: secretToolStore{runSecretTool}}, nil
}
func (KeychainStore) Load() (*State, error) {
	s, err := linuxStore()
	if err != nil {
		return nil, err
	}
	return s.Load()
}
func (KeychainStore) Save(state *State) error {
	s, err := linuxStore()
	if err != nil {
		return err
	}
	return s.Save(state)
}
func (KeychainStore) Delete() error {
	s, err := linuxStore()
	if err != nil {
		return err
	}
	return s.Delete()
}
