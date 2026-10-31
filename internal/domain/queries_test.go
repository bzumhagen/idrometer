package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func TestDashboard(t *testing.T) {
	e := newEnv(t)
	e.ingestN(t, "A1", 3)
	e.ingestN(t, "B2", 1)
	r := e.ingestN(t, "C3", 1)
	if _, err := e.edit.Close(e.ctx, r.FermentationID); err != nil {
		t.Fatal(err)
	}
	q := domain.NewQueries(e.store, e.clock)
	st, err := q.Dashboard(e.ctx)
	if err != nil || len(st) != 3 {
		t.Fatalf("dashboard: %d %v", len(st), err)
	}
	if st[0].Active == nil || st[0].Latest == nil || st[0].Latest.GravityDevice != 1.05 {
		t.Fatalf("A1: %+v", st[0])
	}
	if st[2].Active != nil || st[2].Latest != nil {
		t.Fatalf("closed device should show no active fermentation: %+v", st[2])
	}
}

func TestFermentationDetailSeparatesExcluded(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 6)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := e.edit.SetExcluded(e.ctx, r.FermentationID, start, start.Add(15*time.Minute), true); err != nil {
		t.Fatal(err)
	}
	q := domain.NewQueries(e.store, e.clock)
	d, err := q.Fermentation(e.ctx, r.FermentationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Readings) != 6 || d.ExcludedCount != 2 || d.Stats.Readings != 4 {
		t.Fatalf("readings=%d excluded=%d stats=%d", len(d.Readings), d.ExcludedCount, d.Stats.Readings)
	}
	if _, err := q.Fermentation(e.ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestFermentationListAndMergeCandidates(t *testing.T) {
	e := newEnv(t)
	a := e.ingestN(t, "A1", 2)
	if _, err := e.edit.Close(e.ctx, a.FermentationID); err != nil {
		t.Fatal(err)
	}
	b := e.ingestN(t, "A1", 2)
	e.ingestN(t, "B2", 1)
	q := domain.NewQueries(e.store, e.clock)

	list, err := q.Fermentations(e.ctx, domain.FermentationFilter{})
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	for _, s := range list {
		if s.DeviceName == "" || !s.HasReadings || s.LatestSG != 1.05 {
			t.Fatalf("summary: %+v", s)
		}
	}
	c, err := q.MergeCandidates(e.ctx, b.FermentationID)
	if err != nil || len(c) != 1 || c[0].ID != a.FermentationID {
		t.Fatalf("candidates: %+v %v", c, err)
	}
}

func TestPreferences(t *testing.T) {
	e := newEnv(t)
	ps := domain.NewPreferenceService(e.store, e.events)
	p, err := ps.Get(e.ctx)
	if err != nil || p != domain.DefaultPreferences() {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	want := domain.Preferences{Gravity: domain.DisplayPlato, Temp: domain.TempF, Gap: 48 * time.Hour}
	if err := ps.Update(e.ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := ps.Get(e.ctx); got != want {
		t.Fatalf("got %+v", got)
	}
	// The gap preference is what the ingestor uses.
	r := e.ingestN(t, "A1", 1) // clock now +15m
	e.clock.Advance(49 * time.Hour)
	r2, _ := e.ing.Ingest(e.ctx, input("A1"))
	if r2.FermentationID == r.FermentationID {
		t.Fatal("48h gap preference not applied")
	}
	for _, bad := range []domain.Preferences{
		{Gravity: "brix", Temp: domain.TempC, Gap: time.Hour * 72},
		{Gravity: domain.DisplaySG, Temp: "k", Gap: time.Hour * 72},
		{Gravity: domain.DisplaySG, Temp: domain.TempC, Gap: time.Minute},
	} {
		if err := ps.Update(e.ctx, bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("Update(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestUpdateDevice(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	name, unit := " Fermenter 1 ", domain.GravityPlato
	gap := new(time.Duration)
	*gap = 12 * time.Hour
	d, err := e.edit.UpdateDevice(e.ctx, r.DeviceID, domain.DevicePatch{DisplayName: &name, GapThreshold: &gap, GravityUnit: &unit})
	if err != nil || d.Name() != "Fermenter 1" || *d.GapThreshold != 12*time.Hour || d.GravityUnit != unit {
		t.Fatalf("%+v %v", d, err)
	}
	var none *time.Duration
	d, _ = e.edit.UpdateDevice(e.ctx, r.DeviceID, domain.DevicePatch{GapThreshold: &none})
	if d.GapThreshold != nil {
		t.Fatal("override not cleared")
	}
	tooShort := new(time.Duration)
	*tooShort = time.Minute
	if _, err := e.edit.UpdateDevice(e.ctx, r.DeviceID, domain.DevicePatch{GapThreshold: &tooShort}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("short gap: %v", err)
	}
	bad := domain.GravityUnit("brix")
	if _, err := e.edit.UpdateDevice(e.ctx, r.DeviceID, domain.DevicePatch{GravityUnit: &bad}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad unit: %v", err)
	}
}

func TestDashboardSparkAndSummaryStats(t *testing.T) {
	e := newEnv(t)
	e.ingestN(t, "A1", 20)
	q := domain.NewQueries(e.store, e.clock)
	ds, err := q.Dashboard(e.ctx)
	if err != nil || len(ds) != 1 || len(ds[0].Spark) != 20 {
		t.Fatalf("dashboard: %v %+v", err, ds)
	}
	sums, err := q.Fermentations(e.ctx, domain.FermentationFilter{})
	if err != nil || len(sums) != 1 || sums[0].OG == nil || sums[0].CurrentSG == nil {
		t.Fatalf("summaries: %v %+v", err, sums)
	}
}
