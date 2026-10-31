package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func TestCloseAndReopen(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 3)
	f, err := e.edit.Close(e.ctx, r.FermentationID)
	if err != nil || f.Status != domain.StatusCompleted || f.CloseReason != domain.CloseManual {
		t.Fatalf("close: %+v, %v", f, err)
	}
	if _, err := e.edit.Close(e.ctx, r.FermentationID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("double close: %v", err)
	}
	f, err = e.edit.Reopen(e.ctx, r.FermentationID)
	if err != nil || f.Status != domain.StatusActive || f.EndedAt != nil {
		t.Fatalf("reopen: %+v, %v", f, err)
	}
}

func TestReopenBlockedWhenAnotherIsActive(t *testing.T) {
	e := newEnv(t)
	first := e.ingestN(t, "A1", 2)
	if _, err := e.edit.Close(e.ctx, first.FermentationID); err != nil {
		t.Fatal(err)
	}
	e.ingestN(t, "A1", 1) // starts a second, active fermentation
	if _, err := e.edit.Reopen(e.ctx, first.FermentationID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestUpdateRenames(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	name, notes := "  Hefeweizen  ", "pitched at 19C"
	f, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{Name: &name, Notes: &notes})
	if err != nil || f.Name != "Hefeweizen" || f.Notes != notes {
		t.Fatalf("update: %+v, %v", f, err)
	}
	empty := " "
	if _, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{Name: &empty}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
}

func TestSplit(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 10) // readings at 0,15,...,135 min
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(75 * time.Minute)

	earlier, later, err := e.edit.Split(e.ctx, r.FermentationID, at)
	if err != nil {
		t.Fatal(err)
	}
	if earlier.Status != domain.StatusCompleted || later.Status != domain.StatusActive {
		t.Fatalf("statuses: %v / %v", earlier.Status, later.Status)
	}
	if e.count(t, earlier.ID) != 5 || e.count(t, later.ID) != 5 {
		t.Fatalf("counts: %d / %d", e.count(t, earlier.ID), e.count(t, later.ID))
	}
	if !later.StartedAt.Equal(at) {
		t.Fatalf("later started %v, want %v", later.StartedAt, at)
	}
	// The device still has exactly one active fermentation: the later part.
	if got := e.list(t, domain.FermentationFilter{Status: domain.StatusActive}); len(got) != 1 || got[0].ID != later.ID {
		t.Fatalf("active = %+v", got)
	}
	// Splitting outside the data range is rejected.
	if _, _, err := e.edit.Split(e.ctx, later.ID, at.Add(-24*time.Hour)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("out-of-range split: %v", err)
	}
}

func TestMergeKeepsActiveAndSoftDeletesSource(t *testing.T) {
	e := newEnv(t)
	a := e.ingestN(t, "A1", 3)
	if _, err := e.edit.Close(e.ctx, a.FermentationID); err != nil {
		t.Fatal(err)
	}
	b := e.ingestN(t, "A1", 3) // newer, active

	// Merge the newer (active) one into the older (completed) one.
	dst, err := e.edit.Merge(e.ctx, b.FermentationID, a.FermentationID)
	if err != nil {
		t.Fatal(err)
	}
	if dst.ID != a.FermentationID || dst.Status != domain.StatusActive || dst.EndedAt != nil {
		t.Fatalf("merged = %+v", dst)
	}
	if e.count(t, dst.ID) != 6 || e.count(t, b.FermentationID) != 0 {
		t.Fatalf("counts: dst=%d src=%d", e.count(t, dst.ID), e.count(t, b.FermentationID))
	}
	if got := e.list(t, domain.FermentationFilter{OnlyDeleted: true}); len(got) != 1 || got[0].ID != b.FermentationID {
		t.Fatalf("deleted = %+v", got)
	}
	if !dst.StartedAt.Equal(e.ferm(t, a.FermentationID).StartedAt) {
		t.Fatal("start time should be the earliest")
	}
}

func TestMergeRejections(t *testing.T) {
	e := newEnv(t)
	a := e.ingestN(t, "A1", 2)
	other := e.ingestN(t, "B2", 2)
	if _, err := e.edit.Merge(e.ctx, a.FermentationID, other.FermentationID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("cross-device: %v", err)
	}
	if _, err := e.edit.Merge(e.ctx, a.FermentationID, a.FermentationID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("self: %v", err)
	}

	// Disjoint pieces of one fermentation merge back together.
	e2 := newEnv(t)
	r := e2.ingestN(t, "A1", 6)
	at := e2.clock.t.Add(-6*15*time.Minute + 45*time.Minute)
	earlier, later, err := e2.edit.Split(e2.ctx, r.FermentationID, at)
	if err != nil {
		t.Fatal(err)
	}
	// Merging again fails because the source is now deleted.
	if _, err := e2.edit.Merge(e2.ctx, later.ID, earlier.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.edit.Merge(e2.ctx, later.ID, earlier.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("merging a deleted source: %v", err)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 2)
	if err := e.edit.Delete(e.ctx, r.FermentationID); err != nil {
		t.Fatal(err)
	}
	if got := e.list(t, domain.FermentationFilter{}); len(got) != 0 {
		t.Fatalf("deleted ferm still listed: %+v", got)
	}
	// A new reading starts a fresh fermentation: the deleted one is not "active".
	r2, _ := e.ing.Ingest(e.ctx, input("A1"))
	if r2.FermentationID == r.FermentationID {
		t.Fatal("reading was attached to a deleted fermentation")
	}
	if err := e.edit.Restore(e.ctx, r.FermentationID); err != nil {
		t.Fatal(err)
	}
	if f := e.ferm(t, r.FermentationID); f.DeletedAt != nil || f.Status != domain.StatusCompleted {
		t.Fatalf("restored = %+v (should come back completed)", f)
	}
}

func TestSetExcluded(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 6)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	n, err := e.edit.SetExcluded(e.ctx, r.FermentationID, start.Add(15*time.Minute), start.Add(45*time.Minute), true)
	if err != nil || n != 3 {
		t.Fatalf("excluded %d, err %v", n, err)
	}
	var visible int
	_ = e.store.WithinTx(e.ctx, func(rp domain.Repos) error {
		rs, err := rp.Readings.List(e.ctx, r.FermentationID, false)
		visible = len(rs)
		return err
	})
	if visible != 3 {
		t.Fatalf("visible = %d, want 3", visible)
	}
	if _, err := e.edit.SetExcluded(e.ctx, r.FermentationID, start.Add(time.Hour), start, true); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("reversed range: %v", err)
	}
}
