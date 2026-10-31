package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func TestFirstReadingCreatesDeviceAndFermentation(t *testing.T) {
	e := newEnv(t)
	r, err := e.ing.Ingest(e.ctx, input("A1"))
	if err != nil {
		t.Fatal(err)
	}
	f := e.ferm(t, r.FermentationID)
	if f.Status != domain.StatusActive || f.DeviceID != r.DeviceID {
		t.Fatalf("unexpected fermentation %+v", f)
	}
	if f.Name == "" {
		t.Fatal("expected an auto-generated name")
	}
	if got := len(e.events.events); got != 2 { // fermentation created + reading
		t.Fatalf("events = %d, want 2", got)
	}
}

func TestReadingsWithinGapShareFermentation(t *testing.T) {
	e := newEnv(t)
	first := e.ingestN(t, "A1", 5)
	e.clock.Advance(71 * time.Hour)
	r, err := e.ing.Ingest(e.ctx, input("A1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.FermentationID != first.FermentationID {
		t.Fatal("reading within the gap should stay in the same fermentation")
	}
}

func TestGapStartsNewFermentationAndClosesOld(t *testing.T) {
	e := newEnv(t)
	first := e.ingestN(t, "A1", 4) // last reading at +45m, clock now +60m
	lastAt := first.ReceivedAt
	e.clock.Advance(73 * time.Hour)
	r, err := e.ing.Ingest(e.ctx, input("A1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.FermentationID == first.FermentationID {
		t.Fatal("expected a new fermentation after the gap")
	}
	old := e.ferm(t, first.FermentationID)
	if old.Status != domain.StatusCompleted || old.CloseReason != domain.CloseGap {
		t.Fatalf("old fermentation = %+v", old)
	}
	if old.EndedAt == nil || !old.EndedAt.Equal(first.ReceivedAt) {
		t.Fatalf("ended_at = %v, want last reading %v", old.EndedAt, lastAt)
	}
}

func TestReadingAfterManualCloseStartsNewFermentation(t *testing.T) {
	e := newEnv(t)
	first := e.ingestN(t, "A1", 3)
	if _, err := e.edit.Close(e.ctx, first.FermentationID); err != nil {
		t.Fatal(err)
	}
	r, err := e.ing.Ingest(e.ctx, input("A1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.FermentationID == first.FermentationID {
		t.Fatal("reading after close must start a new fermentation")
	}
}

func TestDevicesAreIndependent(t *testing.T) {
	e := newEnv(t)
	a := e.ingestN(t, "A1", 2)
	b := e.ingestN(t, "B2", 2)
	if a.FermentationID == b.FermentationID || a.DeviceID == b.DeviceID {
		t.Fatal("different devices must not share fermentations")
	}
}

func TestPerDeviceGapOverrideAndGlobalSetting(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)

	// Global setting: 1h.
	_ = e.store.WithinTx(e.ctx, func(rp domain.Repos) error {
		return rp.Settings.Set(e.ctx, domain.SettingGapThreshold, "1h")
	})
	e.clock.Advance(2 * time.Hour)
	r2, _ := e.ing.Ingest(e.ctx, input("A1"))
	if r2.FermentationID == r.FermentationID {
		t.Fatal("global 1h gap not applied")
	}

	// Device override of 10h beats the global 1h.
	_ = e.store.WithinTx(e.ctx, func(rp domain.Repos) error {
		d, _ := rp.Devices.Get(e.ctx, r2.DeviceID)
		g := 10 * time.Hour
		d.GapThreshold = &g
		return rp.Devices.Update(e.ctx, d)
	})
	e.clock.Advance(2 * time.Hour)
	r3, _ := e.ing.Ingest(e.ctx, input("A1"))
	if r3.FermentationID != r2.FermentationID {
		t.Fatal("device override not applied")
	}
}

func TestGravityIsNormalizedToSG(t *testing.T) {
	e := newEnv(t)
	in := input("A1")
	in.Gravity = 12.5 // Plato, auto-detected
	r, err := e.ing.Ingest(e.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.GravityDevice < 1.049 || r.GravityDevice > 1.052 {
		t.Fatalf("gravity = %v, want ~1.050", r.GravityDevice)
	}
}

func TestInvalidInputRejected(t *testing.T) {
	e := newEnv(t)
	bad := input("")
	if _, err := e.ing.Ingest(e.ctx, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing id: err = %v", err)
	}
	hot := input("A1")
	hot.TemperatureC = 400
	if _, err := e.ing.Ingest(e.ctx, hot); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("hot: err = %v", err)
	}
	// Nothing was persisted for the rejected readings.
	if got := len(e.list(t, domain.FermentationFilter{})); got != 0 {
		t.Fatalf("fermentations = %d, want 0", got)
	}
}

func TestSweeperClosesSilentFermentations(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 2)
	e.clock.Advance(10 * time.Hour)
	if n, err := e.sweep.SweepOnce(e.ctx); err != nil || n != 0 {
		t.Fatalf("early sweep: n=%d err=%v", n, err)
	}
	e.clock.Advance(73 * time.Hour)
	if n, err := e.sweep.SweepOnce(e.ctx); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	f := e.ferm(t, r.FermentationID)
	if f.Status != domain.StatusCompleted || f.CloseReason != domain.CloseGap {
		t.Fatalf("fermentation = %+v", f)
	}
}
