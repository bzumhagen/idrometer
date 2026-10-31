package config_test

import (
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/config"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := config.Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.DBPath != "idrometer.db" || c.BackupDir != "backups" ||
		c.BackupInterval != 24*time.Hour || c.BackupKeep != 7 || c.SweepInterval != 15*time.Minute || c.SecureCookies {
		t.Fatalf("%+v", c)
	}
}

func TestBackupDirFollowsDatabase(t *testing.T) {
	c, _ := config.Load(env(map[string]string{"IDROMETER_DB_PATH": "/var/lib/idrometer/idrometer.db"}))
	if c.BackupDir != "/var/lib/idrometer/backups" {
		t.Fatal(c.BackupDir)
	}
	c, _ = config.Load(env(map[string]string{"IDROMETER_DB_PATH": "/x/y.db", "IDROMETER_BACKUP_DIR": "/mnt/nas"}))
	if c.BackupDir != "/mnt/nas" {
		t.Fatal(c.BackupDir)
	}
}

func TestOverrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"IDROMETER_LISTEN": "127.0.0.1:9000", "IDROMETER_SECURE_COOKIES": "true", "IDROMETER_LOG_LEVEL": "DEBUG",
		"IDROMETER_BACKUP_INTERVAL": "off", "IDROMETER_BACKUP_KEEP": "14", "IDROMETER_SWEEP_INTERVAL": "1m",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || !c.SecureCookies || c.LogLevel.String() != "DEBUG" ||
		c.BackupInterval != 0 || c.BackupKeep != 14 || c.SweepInterval != time.Minute {
		t.Fatalf("%+v", c)
	}
}

func TestInvalidValuesAreAllReported(t *testing.T) {
	_, err := config.Load(env(map[string]string{
		"IDROMETER_SECURE_COOKIES": "maybe", "IDROMETER_LOG_LEVEL": "loud", "IDROMETER_BACKUP_INTERVAL": "5m",
		"IDROMETER_BACKUP_KEEP": "0", "IDROMETER_SWEEP_INTERVAL": "soon",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, k := range []string{"SECURE_COOKIES", "LOG_LEVEL", "BACKUP_INTERVAL", "BACKUP_KEEP", "SWEEP_INTERVAL"} {
		if !contains(err.Error(), k) {
			t.Errorf("error does not mention %s: %v", k, err)
		}
	}
}

func TestServeNeedsStrongIngestToken(t *testing.T) {
	c, _ := config.Load(env(map[string]string{"IDROMETER_INGEST_TOKEN": "short"}))
	if c.ValidateServe() == nil {
		t.Fatal("short token accepted")
	}
	c, _ = config.Load(env(map[string]string{"IDROMETER_INGEST_TOKEN": "0123456789abcdef"}))
	if err := c.ValidateServe(); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
