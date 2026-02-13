package app

import (
	"github.com/kelseyhightower/envconfig"
	"github.com/laduwka/matrix-helper/helper"
)

type Config struct {
	Matrix MatrixConfig `envconfig:"MATRIX"`
	Log    LogConfig    `envconfig:"LOG"`
}

type MatrixConfig struct {
	Domain      string `envconfig:"DOMAIN" required:"true"`
	Username    string `envconfig:"USERNAME" required:"true"`
	Password    string `envconfig:"PASSWORD" required:"true"` // #nosec G117 -- read from env, never serialized
	DisplayName string `envconfig:"DISPLAY_NAME"`
	DeviceID    string `envconfig:"DEVICE_ID"`

	HomeserverURL string `envconfig:"-"`
}

func (m MatrixConfig) ToMatrixConfig() helper.Config {
	return helper.Config{
		Domain:        m.Domain,
		Username:      m.Username,
		Password:      m.Password,
		HomeserverURL: m.HomeserverURL,
		DeviceID:      m.DeviceID,
	}
}

type LogConfig struct {
	Level  string `envconfig:"LEVEL" default:"info"`
	Format string `envconfig:"FORMAT" default:"text"`
	File   string `envconfig:"FILE"`
}

func (c *LogConfig) ToLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level:  c.Level,
		Format: c.Format,
		File:   c.File,
	}
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}

	cfg.Matrix.HomeserverURL = "https://" + cfg.Matrix.Domain

	return &cfg, nil
}

func NewDefault() *Config {
	return &Config{
		Matrix: MatrixConfig{
			Domain:        "matrix.bingo-boom.ru",
			Username:      "test_user",
			Password:      "test_password",
			HomeserverURL: "https://matrix.bingo-boom.ru",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func LoadFromValues(domain, username, password string) *Config {
	return &Config{
		Matrix: MatrixConfig{
			Domain:        domain,
			Username:      username,
			Password:      password,
			HomeserverURL: "https://" + domain,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}
