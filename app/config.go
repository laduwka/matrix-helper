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
	Password    string `envconfig:"PASSWORD" required:"true"`
	DisplayName string `envconfig:"DISPLAY_NAME"`

	HomeserverURL string `envconfig:"-"`
}

func (m MatrixConfig) ToMatrixConfig() helper.Config {
	return helper.Config{
		Domain:        m.Domain,
		Username:      m.Username,
		Password:      m.Password,
		HomeserverURL: m.HomeserverURL,
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
			Domain:        "matrix.org",
			Username:      "test_user",
			Password:      "test_password",
			HomeserverURL: "https://matrix.org",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
	}
}
