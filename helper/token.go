package helper

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type CachedToken struct {
	HomeserverURL string    `json:"homeserver_url"`
	UserID        string    `json:"user_id"`
	AccessToken   string    `json:"access_token"`
	DeviceID      string    `json:"device_id"`
	Username      string    `json:"username"`
	Domain        string    `json:"domain"`
	CreatedAt     time.Time `json:"created_at"`
}

func TokenCachePath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir != "" {
		return filepath.Join(dir, "matrix-helper", "token.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "matrix-helper", "token.json")
}

func SaveToken(token *CachedToken) error {
	path := TokenCachePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Wrap(err, "failed to create token cache directory")
	}

	data, err := json.Marshal(token)
	if err != nil {
		return Wrap(err, "failed to marshal token")
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return Wrap(err, "failed to write token cache file")
	}
	return nil
}

func LoadToken() (*CachedToken, error) {
	path := TokenCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, Wrap(err, "failed to read token cache file")
	}

	var token CachedToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, Wrap(err, "failed to unmarshal token")
	}
	return &token, nil
}

func RemoveToken() error {
	path := TokenCachePath()
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Wrap(err, "failed to remove token cache file")
	}
	return nil
}
