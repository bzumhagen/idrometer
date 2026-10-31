// Package config loads settings from IDROMETER_* environment variables. Only
// cmd/idrometer uses it.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen        string
	DBPath        string
	IngestToken   string
	SecureCookies bool
	LogLevel      slog.Level
	SweepInterval time.Duration
	// BackupDir defaults to "backups" beside the database file.
	BackupDir      string
	BackupInterval time.Duration // 0 disables scheduled backups
	BackupKeep     int
}

// Load reads configuration via getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:         or(getenv("IDROMETER_LISTEN"), ":8080"),
		DBPath:         or(getenv("IDROMETER_DB_PATH"), "idrometer.db"),
		IngestToken:    getenv("IDROMETER_INGEST_TOKEN"),
		SweepInterval:  15 * time.Minute,
		BackupDir:      getenv("IDROMETER_BACKUP_DIR"),
		BackupInterval: 24 * time.Hour,
		BackupKeep:     7,
	}
	if c.BackupDir == "" {
		c.BackupDir = filepath.Join(filepath.Dir(c.DBPath), "backups")
	}
	var errs []error
	if v := getenv("IDROMETER_SECURE_COOKIES"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("IDROMETER_SECURE_COOKIES: %w", err))
		}
		c.SecureCookies = b
	}
	if v := getenv("IDROMETER_LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(strings.ToLower(v))); err != nil {
			errs = append(errs, fmt.Errorf("IDROMETER_LOG_LEVEL: %w", err))
		}
	}
	if v := getenv("IDROMETER_SWEEP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			errs = append(errs, fmt.Errorf("IDROMETER_SWEEP_INTERVAL: want a duration of at least 1s, got %q", v))
		} else {
			c.SweepInterval = d
		}
	}
	if v := getenv("IDROMETER_BACKUP_INTERVAL"); v != "" {
		if v == "0" || v == "off" {
			c.BackupInterval = 0
		} else if d, err := time.ParseDuration(v); err != nil || d < time.Hour {
			errs = append(errs, fmt.Errorf("IDROMETER_BACKUP_INTERVAL: want a duration of at least 1h, or 0 to disable; got %q", v))
		} else {
			c.BackupInterval = d
		}
	}
	if v := getenv("IDROMETER_BACKUP_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errs = append(errs, fmt.Errorf("IDROMETER_BACKUP_KEEP: want a positive whole number, got %q", v))
		} else {
			c.BackupKeep = n
		}
	}
	return c, errors.Join(errs...)
}

// ValidateServe checks settings that only the server needs.
func (c Config) ValidateServe() error {
	if len(c.IngestToken) < 16 {
		return errors.New("IDROMETER_INGEST_TOKEN must be set to at least 16 characters")
	}
	return nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// FromEnv is Load(os.Getenv).
func FromEnv() (Config, error) { return Load(os.Getenv) }
