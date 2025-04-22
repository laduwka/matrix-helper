package app

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

type LoggerConfig struct {
	Level  string
	Format string
	File   string
}

type Logger struct {
	*logrus.Logger
}

func NewLogger(cfg LoggerConfig) *logrus.Logger {
	log := logrus.New()

	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	switch cfg.Format {
	case "json":
		log.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02 15:04:05",
		})
	default:
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	var output io.Writer = os.Stdout
	if cfg.File != "" {
		file, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err == nil {
			output = file
		} else {
			log.Warnf("Failed to open log file %s: %v, using stdout", cfg.File, err)
		}
	}
	log.SetOutput(output)

	log.Debug("Logger initialized")
	return log
}

func NewNullLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logger
}
