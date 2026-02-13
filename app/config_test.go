package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_AllVarsSet(t *testing.T) {
	os.Setenv("MATRIX_DOMAIN", "matrix.example.com")
	os.Setenv("MATRIX_USERNAME", "testuser")
	os.Setenv("MATRIX_PASSWORD", "testpass")
	defer func() {
		os.Unsetenv("MATRIX_DOMAIN")
		os.Unsetenv("MATRIX_USERNAME")
		os.Unsetenv("MATRIX_PASSWORD")
	}()

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "matrix.example.com", cfg.Matrix.Domain)
	assert.Equal(t, "testuser", cfg.Matrix.Username)
	assert.Equal(t, "testpass", cfg.Matrix.Password)
	assert.Equal(t, "https://matrix.example.com", cfg.Matrix.HomeserverURL)
}

func TestLoad_DefaultLogConfig(t *testing.T) {
	os.Setenv("MATRIX_DOMAIN", "matrix.example.com")
	os.Setenv("MATRIX_USERNAME", "testuser")
	os.Setenv("MATRIX_PASSWORD", "testpass")
	defer func() {
		os.Unsetenv("MATRIX_DOMAIN")
		os.Unsetenv("MATRIX_USERNAME")
		os.Unsetenv("MATRIX_PASSWORD")
	}()

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
	assert.Empty(t, cfg.Log.File)
}

func TestLoad_MissingRequired(t *testing.T) {
	os.Unsetenv("MATRIX_DOMAIN")
	os.Unsetenv("MATRIX_USERNAME")
	os.Unsetenv("MATRIX_PASSWORD")

	_, err := Load()
	assert.Error(t, err)
}

func TestNewDefault(t *testing.T) {
	cfg := NewDefault()

	assert.Equal(t, "matrix.bingo-boom.ru", cfg.Matrix.Domain)
	assert.Equal(t, "test_user", cfg.Matrix.Username)
	assert.Equal(t, "test_password", cfg.Matrix.Password)
	assert.Equal(t, "https://matrix.bingo-boom.ru", cfg.Matrix.HomeserverURL)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
}

func TestLoadFromValues(t *testing.T) {
	cfg := LoadFromValues("my.server.com", "user1", "pass1")

	assert.Equal(t, "my.server.com", cfg.Matrix.Domain)
	assert.Equal(t, "user1", cfg.Matrix.Username)
	assert.Equal(t, "pass1", cfg.Matrix.Password)
	assert.Equal(t, "https://my.server.com", cfg.Matrix.HomeserverURL)
}

func TestToMatrixConfig(t *testing.T) {
	mc := MatrixConfig{
		Domain:        "matrix.test.com",
		Username:      "user",
		Password:      "pass",
		HomeserverURL: "https://matrix.test.com",
	}

	helperCfg := mc.ToMatrixConfig()
	assert.Equal(t, "matrix.test.com", helperCfg.Domain)
	assert.Equal(t, "user", helperCfg.Username)
	assert.Equal(t, "pass", helperCfg.Password)
	assert.Equal(t, "https://matrix.test.com", helperCfg.HomeserverURL)
}

func TestToLoggerConfig(t *testing.T) {
	lc := LogConfig{
		Level:  "debug",
		Format: "json",
		File:   "/tmp/test.log",
	}

	loggerCfg := lc.ToLoggerConfig()
	assert.Equal(t, "debug", loggerCfg.Level)
	assert.Equal(t, "json", loggerCfg.Format)
	assert.Equal(t, "/tmp/test.log", loggerCfg.File)
}
