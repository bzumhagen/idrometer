// Package storetest is a conformance suite for domain.TxManager
// implementations. Every store adapter must pass it.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// Factory returns a fresh, empty store.
type Factory func(t *testing.T) domain.TxManager

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func Run(t *testing.T, newStore Factory) {
	tests := map[string]func(*testing.T, domain.TxManager){
		"DeviceRoundTrip":           deviceRoundTrip,
		"DeviceUniqueness":          deviceUniqueness,
		"OneActivePerDevice":        oneActivePerDevice,
		"FermentationRoundTrip":     fermentationRoundTrip,
		"ReadingsBoundsMoveExclude": readingsBoundsMoveExclude,
		"RollbackOnError":           rollbackOnError,
		"Settings":                  settingsTest,
		"UsersAndSessions":          usersAndSessions,
		"LatestReading":             latestReading,
		"Recipes":                   recipesTest,
		"ManualAndAnnotations":      manualAndAnnotations,
		"Calibrations":              calibrationsTest,
		"AnnotationTypes":           annotationTypesTest,
		"NotFound":                  notFound,
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) { fn(t, newStore(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func tx(t *testing.T, s domain.TxManager, fn func(r domain.Repos) error) {
	t.Helper()
	must(t, s.WithinTx(context.Background(), fn))
}

func newDevice(t *testing.T, s domain.TxManager, hw string) domain.Device {
	t.Helper()
	d := domain.Device{Source: "ispindel", HardwareID: hw, ReportedName: "n-" + hw, CreatedAt: t0}
	tx(t, s, func(r domain.Repos) error { return r.Devices.Create(context.Background(), &d) })
	return d
}

func newFerm(t *testing.T, s domain.TxManager, dev int64, status domain.FermentationStatus) domain.Fermentation {
	t.Helper()
	f := domain.Fermentation{DeviceID: dev, Name: "f", Status: status, StartedAt: t0}
	tx(t, s, func(r domain.Repos) error { return r.Fermentations.Create(context.Background(), &f) })
	return f
}

func deviceRoundTrip(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	if d.ID == 0 || d.GravityUnit != domain.GravityAuto {
		t.Fatalf("created = %+v", d)
	}
	gap := 36 * time.Hour
	d.GapThreshold, d.DisplayName, d.LastBatteryV, d.LastRSSI = &gap, "Fermenter 1", 3.9, -71
	d.LastSeenAt, d.GravityUnit = t0.Add(time.Minute), domain.GravityPlato
	tx(t, s, func(r domain.Repos) error { return r.Devices.Update(ctx, d) })

	var got domain.Device
	must(t, s.View(ctx, func(r domain.Repos) (err error) {
		got, err = r.Devices.GetBySource(ctx, "ispindel", "A1")
		return
	}))
	if got.GapThreshold == nil || *got.GapThreshold != gap || got.DisplayName != "Fermenter 1" ||
		got.GravityUnit != domain.GravityPlato || got.LastRSSI != -71 || !got.LastSeenAt.Equal(d.LastSeenAt) ||
		!got.CreatedAt.Equal(t0) || got.Name() != "Fermenter 1" {
		t.Fatalf("got %+v", got)
	}
	// Clearing the override round-trips as nil.
	d.GapThreshold = nil
	tx(t, s, func(r domain.Repos) error { return r.Devices.Update(ctx, d) })
	must(t, s.View(ctx, func(r domain.Repos) (err error) { got, err = r.Devices.Get(ctx, d.ID); return }))
	if got.GapThreshold != nil {
		t.Fatalf("gap = %v, want nil", got.GapThreshold)
	}
	var all []domain.Device
	must(t, s.View(ctx, func(r domain.Repos) (err error) { all, err = r.Devices.List(ctx); return }))
	if len(all) != 1 {
		t.Fatalf("list = %d", len(all))
	}
}

func deviceUniqueness(t *testing.T, s domain.TxManager) {
	newDevice(t, s, "A1")
	err := s.WithinTx(context.Background(), func(r domain.Repos) error {
		return r.Devices.Create(context.Background(), &domain.Device{Source: "ispindel", HardwareID: "A1", CreatedAt: t0})
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func oneActivePerDevice(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	other := newDevice(t, s, "B2")
	first := newFerm(t, s, d.ID, domain.StatusActive)
	newFerm(t, s, other.ID, domain.StatusActive) // different device: fine

	err := s.WithinTx(ctx, func(r domain.Repos) error {
		f := domain.Fermentation{DeviceID: d.ID, Name: "dup", Status: domain.StatusActive, StartedAt: t0}
		return r.Fermentations.Create(ctx, &f)
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second active: err = %v, want ErrConflict", err)
	}
	// Completed and soft-deleted ones don't count.
	done := newFerm(t, s, d.ID, domain.StatusCompleted)
	err = s.WithinTx(ctx, func(r domain.Repos) error {
		done.Status = domain.StatusActive
		return r.Fermentations.Update(ctx, done)
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("reopen: err = %v, want ErrConflict", err)
	}
	tx(t, s, func(r domain.Repos) error {
		now := t0
		first.DeletedAt = &now
		return r.Fermentations.Update(ctx, first)
	})
	tx(t, s, func(r domain.Repos) error { // now the slot is free
		done.Status = domain.StatusActive
		return r.Fermentations.Update(ctx, done)
	})
	var act domain.Fermentation
	must(t, s.View(ctx, func(r domain.Repos) (err error) { act, err = r.Fermentations.ActiveForDevice(ctx, d.ID); return }))
	if act.ID != done.ID {
		t.Fatalf("active = %d, want %d", act.ID, done.ID)
	}
}

func fermentationRoundTrip(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	f := newFerm(t, s, d.ID, domain.StatusActive)

	end, og := t0.Add(48*time.Hour), 1.062
	f.Name, f.Notes, f.OGOverride = "Pilsner", "lager", &og
	f.Status, f.EndedAt, f.CloseReason = domain.StatusCompleted, &end, domain.CloseGap
	tx(t, s, func(r domain.Repos) error { return r.Fermentations.Update(ctx, f) })

	var got domain.Fermentation
	must(t, s.View(ctx, func(r domain.Repos) (err error) { got, err = r.Fermentations.Get(ctx, f.ID); return }))
	if got.Name != "Pilsner" || got.Notes != "lager" || got.OGOverride == nil || *got.OGOverride != og ||
		got.EndedAt == nil || !got.EndedAt.Equal(end) || got.CloseReason != domain.CloseGap ||
		got.Status != domain.StatusCompleted || got.DeletedAt != nil || got.RecipeID != nil {
		t.Fatalf("got %+v", got)
	}

	// Filtering.
	newFerm(t, s, d.ID, domain.StatusActive)
	count := func(fl domain.FermentationFilter) (n int) {
		must(t, s.View(ctx, func(r domain.Repos) error {
			l, err := r.Fermentations.List(ctx, fl)
			n = len(l)
			return err
		}))
		return
	}
	if count(domain.FermentationFilter{}) != 2 || count(domain.FermentationFilter{Status: domain.StatusActive}) != 1 ||
		count(domain.FermentationFilter{OnlyDeleted: true}) != 0 || count(domain.FermentationFilter{DeviceID: 999}) != 0 {
		t.Fatal("filter counts wrong")
	}
	now := t0
	got.DeletedAt = &now
	tx(t, s, func(r domain.Repos) error { return r.Fermentations.Update(ctx, got) })
	if count(domain.FermentationFilter{}) != 1 || count(domain.FermentationFilter{OnlyDeleted: true}) != 1 ||
		count(domain.FermentationFilter{IncludeDeleted: true}) != 2 {
		t.Fatal("deleted filter counts wrong")
	}
}

func readingsBoundsMoveExclude(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	a := newFerm(t, s, d.ID, domain.StatusCompleted)
	b := newFerm(t, s, d.ID, domain.StatusCompleted)

	tx(t, s, func(r domain.Repos) error {
		for i := 0; i < 6; i++ {
			rd := domain.Reading{
				DeviceID: d.ID, FermentationID: a.ID, ReceivedAt: t0.Add(time.Duration(i) * time.Hour),
				Angle: 30 + float64(i), TemperatureC: 20, BatteryV: 4, GravityDevice: 1.05, RSSI: -60,
				IntervalS: 900, RawPayload: []byte(`{"x":1}`),
			}
			if err := r.Readings.Insert(ctx, &rd); err != nil {
				return err
			}
			if rd.ID == 0 {
				t.Error("insert did not set ID")
			}
		}
		return nil
	})

	var lo, hi time.Time
	var ok bool
	must(t, s.View(ctx, func(r domain.Repos) (err error) { lo, hi, ok, err = r.Readings.Bounds(ctx, a.ID); return }))
	if !ok || !lo.Equal(t0) || !hi.Equal(t0.Add(5*time.Hour)) {
		t.Fatalf("bounds = %v %v %v", lo, hi, ok)
	}
	must(t, s.View(ctx, func(r domain.Repos) (err error) { _, _, ok, err = r.Readings.Bounds(ctx, b.ID); return }))
	if ok {
		t.Fatal("empty fermentation should report ok=false")
	}

	tx(t, s, func(r domain.Repos) error { return r.Readings.MoveSince(ctx, a.ID, b.ID, t0.Add(3*time.Hour)) })
	var la, lb []domain.Reading
	must(t, s.View(ctx, func(r domain.Repos) (err error) {
		if la, err = r.Readings.List(ctx, a.ID, true); err != nil {
			return
		}
		lb, err = r.Readings.List(ctx, b.ID, true)
		return
	}))
	if len(la) != 3 || len(lb) != 3 || !lb[0].ReceivedAt.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("after move: %d / %d", len(la), len(lb))
	}
	if string(lb[0].RawPayload) != `{"x":1}` || lb[0].Angle != 33 || lb[0].RSSI != -60 || lb[0].DeviceID != d.ID {
		t.Fatalf("reading fields lost: %+v", lb[0])
	}

	var n int64
	tx(t, s, func(r domain.Repos) (err error) {
		n, err = r.Readings.SetExcluded(ctx, b.ID, t0.Add(3*time.Hour), t0.Add(4*time.Hour), true)
		return
	})
	if n != 2 {
		t.Fatalf("excluded %d, want 2", n)
	}
	tx(t, s, func(r domain.Repos) (err error) { // idempotent
		n, err = r.Readings.SetExcluded(ctx, b.ID, t0.Add(3*time.Hour), t0.Add(4*time.Hour), true)
		return
	})
	if n != 0 {
		t.Fatalf("re-excluding changed %d rows", n)
	}
	must(t, s.View(ctx, func(r domain.Repos) (err error) { lb, err = r.Readings.List(ctx, b.ID, false); return }))
	if len(lb) != 1 {
		t.Fatalf("visible = %d, want 1", len(lb))
	}

	tx(t, s, func(r domain.Repos) error { return r.Readings.MoveAll(ctx, b.ID, a.ID) })
	must(t, s.View(ctx, func(r domain.Repos) (err error) { la, err = r.Readings.List(ctx, a.ID, true); return }))
	if len(la) != 6 {
		t.Fatalf("after MoveAll: %d", len(la))
	}
}

func rollbackOnError(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.WithinTx(ctx, func(r domain.Repos) error {
		d := domain.Device{Source: "ispindel", HardwareID: "ghost", CreatedAt: t0}
		if err := r.Devices.Create(ctx, &d); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	err = s.View(ctx, func(r domain.Repos) error {
		_, err := r.Devices.GetBySource(ctx, "ispindel", "ghost")
		return err
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("device survived rollback: %v", err)
	}
}

func settingsTest(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	err := s.View(ctx, func(r domain.Repos) error { _, err := r.Settings.Get(ctx, "k"); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unset: %v", err)
	}
	tx(t, s, func(r domain.Repos) error { return r.Settings.Set(ctx, "k", "v1") })
	tx(t, s, func(r domain.Repos) error { return r.Settings.Set(ctx, "k", "v2") })
	var v string
	must(t, s.View(ctx, func(r domain.Repos) (err error) { v, err = r.Settings.Get(ctx, "k"); return }))
	if v != "v2" {
		t.Fatalf("v = %q", v)
	}
}

func notFound(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	err := s.View(ctx, func(r domain.Repos) error {
		if _, err := r.Devices.Get(ctx, 42); !errors.Is(err, domain.ErrNotFound) {
			return errors.New("device: " + errString(err))
		}
		if _, err := r.Fermentations.Get(ctx, 42); !errors.Is(err, domain.ErrNotFound) {
			return errors.New("fermentation: " + errString(err))
		}
		if _, err := r.Fermentations.ActiveForDevice(ctx, 42); !errors.Is(err, domain.ErrNotFound) {
			return errors.New("active: " + errString(err))
		}
		return nil
	})
	must(t, err)
	err = s.WithinTx(ctx, func(r domain.Repos) error { return r.Devices.Update(ctx, domain.Device{ID: 42}) })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return "nil error"
	}
	return err.Error()
}

func usersAndSessions(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	err := s.View(ctx, func(r domain.Repos) error { _, err := r.Users.Get(ctx, "admin"); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	tx(t, s, func(r domain.Repos) error {
		return r.Users.Upsert(ctx, domain.User{Username: "admin", PasswordHash: "h1", CreatedAt: t0})
	})
	tx(t, s, func(r domain.Repos) error { // replaces the hash, keeps one row
		return r.Users.Upsert(ctx, domain.User{Username: "admin", PasswordHash: "h2", CreatedAt: t0.Add(time.Hour)})
	})
	var u domain.User
	must(t, s.View(ctx, func(r domain.Repos) (err error) { u, err = r.Users.Get(ctx, "admin"); return }))
	if u.PasswordHash != "h2" || !u.CreatedAt.Equal(t0) {
		t.Fatalf("user = %+v", u)
	}

	sess := func(h string, exp time.Time) domain.Session {
		return domain.Session{TokenHash: h, Username: "admin", CreatedAt: t0, ExpiresAt: exp}
	}
	tx(t, s, func(r domain.Repos) error {
		if err := r.Sessions.Create(ctx, sess("old", t0.Add(time.Hour))); err != nil {
			return err
		}
		return r.Sessions.Create(ctx, sess("new", t0.Add(48*time.Hour)))
	})
	err = s.WithinTx(ctx, func(r domain.Repos) error { return r.Sessions.Create(ctx, sess("new", t0)) })
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate session: %v", err)
	}
	tx(t, s, func(r domain.Repos) error { return r.Sessions.DeleteExpired(ctx, t0.Add(2*time.Hour)) })
	has := func(h string) bool {
		var ok bool
		must(t, s.View(ctx, func(r domain.Repos) error {
			_, err := r.Sessions.Get(ctx, h)
			ok = err == nil
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			return nil
		}))
		return ok
	}
	if has("old") || !has("new") {
		t.Fatalf("expiry sweep wrong: old=%v new=%v", has("old"), has("new"))
	}
	tx(t, s, func(r domain.Repos) error { return r.Sessions.DeleteForUser(ctx, "admin") })
	if has("new") {
		t.Fatal("DeleteForUser left a session")
	}
}

func latestReading(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	f := newFerm(t, s, d.ID, domain.StatusActive)
	err := s.View(ctx, func(r domain.Repos) error { _, err := r.Readings.Latest(ctx, f.ID); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	tx(t, s, func(r domain.Repos) error {
		for i, g := range []float64{1.05, 1.04, 1.03} {
			rd := domain.Reading{DeviceID: d.ID, FermentationID: f.ID, ReceivedAt: t0.Add(time.Duration(i) * time.Hour),
				TemperatureC: 20, GravityDevice: g}
			if err := r.Readings.Insert(ctx, &rd); err != nil {
				return err
			}
		}
		return nil
	})
	var got domain.Reading
	must(t, s.View(ctx, func(r domain.Repos) (err error) { got, err = r.Readings.Latest(ctx, f.ID); return }))
	if got.GravityDevice != 1.03 || !got.ReceivedAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("latest = %+v", got)
	}
}

func recipesTest(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	og := 1.052
	r := domain.Recipe{Name: "weizen", Style: "Hefeweizen", TargetOG: &og, Yeast: "3068", Notes: "n", CreatedAt: t0, UpdatedAt: t0}
	tx(t, s, func(rp domain.Repos) error { return rp.Recipes.Create(ctx, &r) })
	tx(t, s, func(rp domain.Repos) error {
		z := domain.Recipe{Name: "Altbier", CreatedAt: t0, UpdatedAt: t0}
		return rp.Recipes.Create(ctx, &z)
	})
	r.Name, r.UpdatedAt = "Weizen v2", t0.Add(time.Hour)
	tx(t, s, func(rp domain.Repos) error { return rp.Recipes.Update(ctx, r) })

	var got domain.Recipe
	must(t, s.View(ctx, func(rp domain.Repos) (err error) { got, err = rp.Recipes.Get(ctx, r.ID); return }))
	if got.Name != "Weizen v2" || got.TargetOG == nil || *got.TargetOG != og || got.TargetFG != nil ||
		!got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("got %+v", got)
	}
	var all []domain.Recipe
	must(t, s.View(ctx, func(rp domain.Repos) (err error) { all, err = rp.Recipes.List(ctx); return }))
	if len(all) != 2 || all[0].Name != "Altbier" { // case-insensitive name order
		t.Fatalf("list = %+v", all)
	}

	// Deleting a recipe unlinks it from fermentations but keeps them.
	d := newDevice(t, s, "A1")
	f := newFerm(t, s, d.ID, domain.StatusCompleted)
	f.RecipeID = &r.ID
	tx(t, s, func(rp domain.Repos) error { return rp.Fermentations.Update(ctx, f) })
	tx(t, s, func(rp domain.Repos) error { return rp.Recipes.Delete(ctx, r.ID) })
	var kept domain.Fermentation
	must(t, s.View(ctx, func(rp domain.Repos) (err error) { kept, err = rp.Fermentations.Get(ctx, f.ID); return }))
	if kept.RecipeID != nil {
		t.Fatalf("recipe link survived delete: %v", *kept.RecipeID)
	}
	if err := s.WithinTx(ctx, func(rp domain.Repos) error { return rp.Recipes.Delete(ctx, r.ID) }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func manualAndAnnotations(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	a := newFerm(t, s, d.ID, domain.StatusCompleted)
	b := newFerm(t, s, d.ID, domain.StatusCompleted)

	temp := 20.5
	var m1, m2 domain.ManualReading
	tx(t, s, func(r domain.Repos) error {
		m1 = domain.ManualReading{FermentationID: a.ID, TakenAt: t0, GravitySG: 1.055, TemperatureC: &temp,
			Method: domain.MethodHydrometer, IsOG: true, Note: "OG"}
		m2 = domain.ManualReading{FermentationID: a.ID, TakenAt: t0.Add(48 * time.Hour), GravitySG: 1.012, Method: domain.MethodRefractometer, IsFG: true}
		if err := r.Manual.Create(ctx, &m1); err != nil {
			return err
		}
		if err := r.Manual.Create(ctx, &m2); err != nil {
			return err
		}
		return r.Annotations.Create(ctx, &domain.Annotation{FermentationID: a.ID, At: t0.Add(24 * time.Hour), Kind: "Dry hop", Note: "Citra"})
	})
	var ms []domain.ManualReading
	must(t, s.View(ctx, func(r domain.Repos) (err error) { ms, err = r.Manual.List(ctx, a.ID); return }))
	if len(ms) != 2 || !ms[0].IsOG || ms[0].TemperatureC == nil || *ms[0].TemperatureC != temp || ms[1].TemperatureC != nil ||
		ms[1].Method != domain.MethodRefractometer || !ms[1].IsFG || !ms[0].TakenAt.Equal(t0) {
		t.Fatalf("manual = %+v", ms)
	}
	m2.GravitySG, m2.IsFG = 1.010, false
	tx(t, s, func(r domain.Repos) error { return r.Manual.Update(ctx, m2) })
	var got domain.ManualReading
	must(t, s.View(ctx, func(r domain.Repos) (err error) { got, err = r.Manual.Get(ctx, m2.ID); return }))
	if got.GravitySG != 1.010 || got.IsFG {
		t.Fatalf("updated = %+v", got)
	}

	// Moving: since-time moves the later items; MoveAll moves the rest.
	tx(t, s, func(r domain.Repos) error {
		if err := r.Manual.MoveSince(ctx, a.ID, b.ID, t0.Add(time.Hour)); err != nil {
			return err
		}
		return r.Annotations.MoveSince(ctx, a.ID, b.ID, t0.Add(time.Hour))
	})
	var la, lb []domain.ManualReading
	var na, nb []domain.Annotation
	must(t, s.View(ctx, func(r domain.Repos) (err error) {
		if la, err = r.Manual.List(ctx, a.ID); err != nil {
			return
		}
		if lb, err = r.Manual.List(ctx, b.ID); err != nil {
			return
		}
		if na, err = r.Annotations.List(ctx, a.ID); err != nil {
			return
		}
		nb, err = r.Annotations.List(ctx, b.ID)
		return
	}))
	if len(la) != 1 || len(lb) != 1 || len(na) != 0 || len(nb) != 1 || nb[0].Kind != "Dry hop" || nb[0].Note != "Citra" {
		t.Fatalf("after MoveSince: %d %d / %d %d", len(la), len(lb), len(na), len(nb))
	}
	tx(t, s, func(r domain.Repos) error {
		if err := r.Manual.MoveAll(ctx, a.ID, b.ID); err != nil {
			return err
		}
		return r.Annotations.MoveAll(ctx, b.ID, a.ID)
	})
	must(t, s.View(ctx, func(r domain.Repos) (err error) {
		if lb, err = r.Manual.List(ctx, b.ID); err != nil {
			return
		}
		na, err = r.Annotations.List(ctx, a.ID)
		return
	}))
	if len(lb) != 2 || len(na) != 1 {
		t.Fatalf("after MoveAll: %d %d", len(lb), len(na))
	}

	tx(t, s, func(r domain.Repos) error {
		if err := r.Manual.Delete(ctx, m1.ID); err != nil {
			return err
		}
		return r.Annotations.Delete(ctx, na[0].ID)
	})
	err := s.WithinTx(ctx, func(r domain.Repos) error { return r.Manual.Delete(ctx, m1.ID) })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}

func calibrationsTest(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	d := newDevice(t, s, "A1")
	err := s.View(ctx, func(r domain.Repos) error { _, err := r.Calibrations.Get(ctx, d.ID); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unset: %v", err)
	}
	c := domain.Calibration{DeviceID: d.ID, C: [4]float64{1, 0.001, -2e-6, 3e-9}, OutputUnit: domain.GravitySG, Note: "v1", UpdatedAt: t0}
	tx(t, s, func(r domain.Repos) error { return r.Calibrations.Set(ctx, c) })
	c.C[0], c.Note, c.OutputUnit = 1.5, "v2", domain.GravityPlato
	tx(t, s, func(r domain.Repos) error { return r.Calibrations.Set(ctx, c) }) // replaces
	var got domain.Calibration
	must(t, s.View(ctx, func(r domain.Repos) (err error) { got, err = r.Calibrations.Get(ctx, d.ID); return }))
	if got.C != c.C || got.Note != "v2" || got.OutputUnit != domain.GravityPlato || !got.UpdatedAt.Equal(t0) {
		t.Fatalf("got %+v", got)
	}
	tx(t, s, func(r domain.Repos) error { return r.Calibrations.Delete(ctx, d.ID) })
	tx(t, s, func(r domain.Repos) error { return r.Calibrations.Delete(ctx, d.ID) }) // idempotent
	err = s.View(ctx, func(r domain.Repos) error { _, err := r.Calibrations.Get(ctx, d.ID); return err })
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func annotationTypesTest(t *testing.T, s domain.TxManager) {
	ctx := context.Background()
	var types []domain.AnnotationType
	tx(t, s, func(rp domain.Repos) (err error) { types, err = rp.AnnotationTypes.List(ctx); return })
	if len(types) != len(domain.DefaultAnnotationTypes) || types[0].Name != domain.DefaultAnnotationTypes[0] {
		t.Fatalf("defaults: %+v", types)
	}

	// Case-insensitive uniqueness; new types go last.
	nt := domain.AnnotationType{Name: "Added fruit"}
	tx(t, s, func(rp domain.Repos) error { return rp.AnnotationTypes.Create(ctx, &nt) })
	err := s.WithinTx(ctx, func(rp domain.Repos) error {
		dup := domain.AnnotationType{Name: "added FRUIT"}
		return rp.AnnotationTypes.Create(ctx, &dup)
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}

	// Renaming relabels events already logged with the old name.
	var fid int64
	tx(t, s, func(rp domain.Repos) error {
		d := domain.Device{Source: "ispindel", HardwareID: "e1", ReportedName: "e1"}
		if err := rp.Devices.Create(ctx, &d); err != nil {
			return err
		}
		f := domain.Fermentation{DeviceID: d.ID, Name: "f", Status: domain.StatusActive, StartedAt: t0}
		if err := rp.Fermentations.Create(ctx, &f); err != nil {
			return err
		}
		fid = f.ID
		return rp.Annotations.Create(ctx, &domain.Annotation{FermentationID: f.ID, At: t0, Kind: "Added fruit"})
	})
	tx(t, s, func(rp domain.Repos) error { return rp.AnnotationTypes.Rename(ctx, nt.ID, "Fruit addition") })
	var as []domain.Annotation
	tx(t, s, func(rp domain.Repos) (err error) { as, err = rp.Annotations.List(ctx, fid); return })
	if len(as) != 1 || as[0].Kind != "Fruit addition" {
		t.Fatalf("relabel: %+v", as)
	}
	if err := s.WithinTx(ctx, func(rp domain.Repos) error { return rp.AnnotationTypes.Rename(ctx, nt.ID, "dry hop") }); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("rename onto existing: %v", err)
	}
	if err := s.WithinTx(ctx, func(rp domain.Repos) error { return rp.AnnotationTypes.Rename(ctx, 9999, "x") }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rename missing: %v", err)
	}

	// Deleting a type leaves logged events alone.
	tx(t, s, func(rp domain.Repos) error { return rp.AnnotationTypes.Delete(ctx, nt.ID) })
	tx(t, s, func(rp domain.Repos) (err error) { as, err = rp.Annotations.List(ctx, fid); return })
	if len(as) != 1 || as[0].Kind != "Fruit addition" {
		t.Fatalf("after delete: %+v", as)
	}
	if err := s.WithinTx(ctx, func(rp domain.Repos) error { return rp.AnnotationTypes.Delete(ctx, nt.ID) }); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
