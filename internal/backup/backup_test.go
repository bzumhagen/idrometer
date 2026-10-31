package backup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/backup"
)

type fakeSnap struct {
	fail  bool
	calls int
}

func (f *fakeSnap) Snapshot(_ context.Context, dest string) error {
	f.calls++
	if f.fail {
		_ = os.WriteFile(dest, []byte("partial"), 0o644) // a failed copy may leave junk behind
		return errors.New("disk full")
	}
	return os.WriteFile(dest, []byte("data"), 0o644)
}

func newSvc(t *testing.T, keep int) (*backup.Service, *fakeSnap, *time.Time) {
	t.Helper()
	snap := &fakeSnap{}
	now := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	return &backup.Service{
		Snap: snap, Dir: filepath.Join(t.TempDir(), "backups"), Interval: 24 * time.Hour, Keep: keep,
		Now: func() time.Time { return now },
	}, snap, &now
}

func TestOnceWritesPrivateFileAndKeepsNewest(t *testing.T) {
	s, _, now := newSvc(t, 3)
	var last string
	for i := 0; i < 5; i++ {
		var err error
		if last, err = s.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(24 * time.Hour)
	}
	names, _ := s.List()
	if len(names) != 3 || names[0] != "idrometer-20261003T030000Z.db" || names[2] != "idrometer-20261005T030000Z.db" {
		t.Fatalf("kept %v", names)
	}
	fi, err := os.Stat(last)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v err %v", fi.Mode(), err)
	}
	if di, _ := os.Stat(s.Dir); di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode())
	}
}

func TestFailedSnapshotLeavesNoFiles(t *testing.T) {
	s, snap, _ := newSvc(t, 3)
	snap.fail = true
	if _, err := s.Once(context.Background()); err == nil {
		t.Fatal("want error")
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestRunSnapshotsOnStartOnlyWhenDue(t *testing.T) {
	s, snap, now := newSvc(t, 7)
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		s.Run(ctx)
	}
	run()
	if snap.calls != 1 {
		t.Fatalf("first start: %d snapshots", snap.calls)
	}
	*now = now.Add(2 * time.Hour) // restart soon after: not due
	run()
	if snap.calls != 1 {
		t.Fatalf("restart within interval: %d snapshots", snap.calls)
	}
	*now = now.Add(25 * time.Hour)
	run()
	if snap.calls != 2 {
		t.Fatalf("after interval: %d snapshots", snap.calls)
	}
}

func TestDisabledWhenIntervalIsZero(t *testing.T) {
	s, snap, _ := newSvc(t, 7)
	s.Interval = 0
	s.Run(context.Background()) // returns immediately
	if snap.calls != 0 {
		t.Fatal("disabled service took a snapshot")
	}
}
