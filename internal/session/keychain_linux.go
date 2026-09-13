package session

import (
	"os"
	"path/filepath"
)

func linuxSessionDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(dir, "line-cli"))
}

func linuxStore() (secretFileStore, error) {
	dir, err := linuxSessionDir()
	if err != nil {
		return secretFileStore{}, err
	}
	return secretFileStore{path: filepath.Join(dir, "session.enc"), secrets: secretToolStore{run: runSecretTool}}, nil
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
