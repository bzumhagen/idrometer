// Package backup takes scheduled snapshots of the database and keeps the
// newest few. Snapshots are consistent copies made while the service runs, so
// Proxmox vzdump/PBS backups of the container always contain a clean file even
// if they catch the live database mid-write.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Snapshotter writes a consistent copy of the database to dest.
type Snapshotter interface {
	Snapshot(ctx context.Context, dest string) error
}

const (
	prefix = "idrometer-"
	suffix = ".db"
	stamp  = "20060102T150405Z"
)

type Service struct {
	Snap     Snapshotter
	Dir      string
	Interval time.Duration
	Keep     int
	Now      func() time.Time
	Log      *slog.Logger
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Once writes one snapshot and prunes old ones. It returns the new file's path.
func (s *Service) Once(ctx context.Context) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(s.Dir, prefix+s.now().Format(stamp)+suffix)
	tmp := final + ".tmp"
	_ = os.Remove(tmp)
	if err := s.Snap.Snapshot(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("snapshot: %w", err)
	}
	// Snapshots hold the password hash, so keep them private.
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := s.prune(); err != nil {
		s.log().Warn("backup pruning failed", "err", err)
	}
	return final, nil
}

// List returns existing snapshot files, oldest first.
func (s *Service) List() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, suffix) {
			names = append(names, n)
		}
	}
	slices.Sort(names) // timestamps sort lexically
	return names, nil
}

func (s *Service) prune() error {
	if s.Keep <= 0 {
		return nil
	}
	names, err := s.List()
	if err != nil || len(names) <= s.Keep {
		return err
	}
	for _, n := range names[:len(names)-s.Keep] {
		if err := os.Remove(filepath.Join(s.Dir, n)); err != nil {
			return err
		}
	}
	return nil
}

// due reports whether the newest snapshot is older than the interval.
func (s *Service) due() bool {
	names, err := s.List()
	if err != nil || len(names) == 0 {
		return true
	}
	t, err := time.Parse(stamp, strings.TrimSuffix(strings.TrimPrefix(names[len(names)-1], prefix), suffix))
	return err != nil || s.now().Sub(t) >= s.Interval
}

// Run takes a snapshot at start-up if one is due, then one every Interval,
// until ctx is cancelled. An Interval of zero or less disables it.
func (s *Service) Run(ctx context.Context) {
	if s.Interval <= 0 {
		s.log().Info("scheduled backups disabled")
		return
	}
	check := min(s.Interval, time.Hour)
	t := time.NewTicker(check)
	defer t.Stop()
	for {
		if s.due() {
			if path, err := s.Once(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				s.log().Error("backup failed", "err", err)
			} else {
				s.log().Info("backup written", "path", path)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
