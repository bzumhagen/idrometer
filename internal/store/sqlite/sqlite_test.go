package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/store/sqlite"
	"github.com/bzumhagen/idrometer/internal/store/storetest"
)

func open(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) domain.TxManager {
		return open(t, filepath.Join(t.TempDir(), "test.db"))
	})
}

func TestReopenKeepsDataAndMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithinTx(ctx, func(r domain.Repos) error { return r.Settings.Set(ctx, "k", "v") }); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2 := open(t, path)
	var v string
	if err := s2.View(ctx, func(r domain.Repos) (err error) { v, err = r.Settings.Get(ctx, "k"); return }); err != nil || v != "v" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}

func TestSnapshotIsUsableCopy(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s := open(t, filepath.Join(dir, "live.db"))
	if err := s.WithinTx(ctx, func(r domain.Repos) error { return r.Settings.Set(ctx, "k", "v") }); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "snap.db")
	if err := s.Snapshot(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() == 0 {
		t.Fatalf("snapshot missing: %v", err)
	}
	snap := open(t, dest)
	var v string
	if err := snap.View(ctx, func(r domain.Repos) (err error) { v, err = r.Settings.Get(ctx, "k"); return }); err != nil || v != "v" {
		t.Fatalf("snapshot v=%q err=%v", v, err)
	}
}

// End-to-end session logic against the real store.
func TestIngestorOnSQLite(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "test.db"))
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := &clock{now}
	ing := domain.NewIngestor(s, clock, domain.NopPublisher{})
	in := domain.ReadingInput{Source: "ispindel", HardwareID: "A1", Name: "iSpindel", TemperatureC: 20, Gravity: 1.05}

	r1, err := ing.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(time.Hour)
	r2, _ := ing.Ingest(ctx, in)
	clock.t = clock.t.Add(80 * time.Hour)
	r3, err := ing.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if r1.FermentationID != r2.FermentationID || r2.FermentationID == r3.FermentationID {
		t.Fatalf("fermentation ids: %d %d %d", r1.FermentationID, r2.FermentationID, r3.FermentationID)
	}
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func TestOpenAcceptsRelativePath(t *testing.T) {
	t.Chdir(t.TempDir())
	s, err := sqlite.Open(context.Background(), "./rel.db")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat("rel.db"); err != nil {
		t.Fatal(err)
	}
}
