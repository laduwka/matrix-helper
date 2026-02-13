package helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCachePath_XDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	path := TokenCachePath()
	assert.Equal(t, filepath.Join(dir, "matrix-helper", "token.json"), path)
}

func TestTokenCachePath_FallbackToHome(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	path := TokenCachePath()
	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".cache", "matrix-helper", "token.json"), path)
}

func TestSaveAndLoadToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	token := &CachedToken{
		HomeserverURL: "https://matrix.example.com",
		UserID:        "@user:example.com",
		AccessToken:   "syt_secret_token",
		DeviceID:      "ABCDEF",
		Username:      "user",
		Domain:        "matrix.example.com",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}

	err := SaveToken(token)
	require.NoError(t, err)

	// Verify file permissions
	path := TokenCachePath()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Verify directory permissions
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	// Load and verify
	loaded, err := LoadToken()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, token.HomeserverURL, loaded.HomeserverURL)
	assert.Equal(t, token.UserID, loaded.UserID)
	assert.Equal(t, token.AccessToken, loaded.AccessToken)
	assert.Equal(t, token.DeviceID, loaded.DeviceID)
	assert.Equal(t, token.Username, loaded.Username)
	assert.Equal(t, token.Domain, loaded.Domain)
	assert.True(t, token.CreatedAt.Equal(loaded.CreatedAt))
}

func TestLoadToken_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	loaded, err := LoadToken()
	assert.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestRemoveToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	token := &CachedToken{
		HomeserverURL: "https://matrix.example.com",
		UserID:        "@user:example.com",
		AccessToken:   "syt_secret_token",
		DeviceID:      "ABCDEF",
		Username:      "user",
		Domain:        "matrix.example.com",
		CreatedAt:     time.Now(),
	}

	err := SaveToken(token)
	require.NoError(t, err)

	err = RemoveToken()
	require.NoError(t, err)

	loaded, err := LoadToken()
	assert.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestRemoveToken_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	err := RemoveToken()
	assert.NoError(t, err)
}
