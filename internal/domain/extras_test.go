package domain_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func f64(v float64) *float64 { return &v }

func TestCalibrationPolynomial(t *testing.T) {
	c := domain.Calibration{C: [4]float64{1.0, 0.001, 0, 0}, OutputUnit: domain.GravitySG}
	if g := c.Gravity(40); math.Abs(g-1.04) > 1e-9 {
		t.Fatalf("sg: %v", g)
	}
	cubic := domain.Calibration{C: [4]float64{1, 0.002, 0.0001, 0.000001}, OutputUnit: domain.GravitySG}
	want := 1 + 0.002*10 + 0.0001*100 + 0.000001*1000
	if g := cubic.Gravity(10); math.Abs(g-want) > 1e-12 {
		t.Fatalf("cubic: %v want %v", g, want)
	}
	plato := domain.Calibration{C: [4]float64{0, 0.3, 0, 0}, OutputUnit: domain.GravityPlato}
	if g := plato.Gravity(40); math.Abs(g-domain.PlatoToSG(12)) > 1e-9 {
		t.Fatalf("plato: %v", g)
	}
}

func TestCalibrationRecalibratesPastReadings(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 3) // angle 40, device gravity 1.050
	q := domain.NewQueries(e.store, e.clock)

	d, _ := q.Fermentation(e.ctx, r.FermentationID)
	if d.Readings[0].Gravity != 1.050 || d.Calibration != nil {
		t.Fatalf("uncalibrated: %+v", d.Readings[0])
	}
	cal := domain.Calibration{DeviceID: r.DeviceID, C: [4]float64{1.0, 0.001, 0, 0}, OutputUnit: domain.GravitySG}
	if err := e.edit.SetCalibration(e.ctx, cal); err != nil {
		t.Fatal(err)
	}
	d, _ = q.Fermentation(e.ctx, r.FermentationID)
	if math.Abs(d.Readings[0].Gravity-1.040) > 1e-9 || d.Readings[0].GravityDevice != 1.050 {
		t.Fatalf("calibrated gravity %v (device value must be kept: %v)", d.Readings[0].Gravity, d.Readings[0].GravityDevice)
	}
	if math.Abs(*d.Stats.CurrentSG-1.040) > 1e-9 {
		t.Fatalf("stats ignore calibration: %v", *d.Stats.CurrentSG)
	}
	// New readings use it too, including the one handed to live subscribers.
	rd, _ := e.ing.Ingest(e.ctx, input("A1"))
	if math.Abs(rd.Gravity-1.040) > 1e-9 {
		t.Fatalf("live reading gravity %v", rd.Gravity)
	}
	if dash, _ := q.Dashboard(e.ctx); math.Abs(dash[0].Latest.Gravity-1.040) > 1e-9 {
		t.Fatalf("dashboard gravity %v", dash[0].Latest.Gravity)
	}

	if err := e.edit.ClearCalibration(e.ctx, r.DeviceID); err != nil {
		t.Fatal(err)
	}
	d, _ = q.Fermentation(e.ctx, r.FermentationID)
	if d.Readings[0].Gravity != 1.050 {
		t.Fatalf("after clearing: %v", d.Readings[0].Gravity)
	}
}

func TestCalibrationValidation(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	for _, c := range []domain.Calibration{
		{DeviceID: r.DeviceID, OutputUnit: domain.GravitySG}, // all zero
		{DeviceID: r.DeviceID, C: [4]float64{math.NaN(), 1, 0, 0}, OutputUnit: domain.GravitySG},
		{DeviceID: r.DeviceID, C: [4]float64{1, 1, 0, 0}, OutputUnit: "brix"},
	} {
		if err := e.edit.SetCalibration(e.ctx, c); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if err := e.edit.SetCalibration(e.ctx, domain.Calibration{DeviceID: 999, C: [4]float64{1, 1, 0, 0}, OutputUnit: domain.GravitySG}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
}

func TestManualReadingsDriveOGAndFG(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 4)
	q := domain.NewQueries(e.store, e.clock)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	if _, err := e.edit.AddManualReading(e.ctx, domain.ManualReading{
		FermentationID: r.FermentationID, TakenAt: start, GravitySG: 1.060, IsOG: true, Method: domain.MethodRefractometer,
	}); err != nil {
		t.Fatal(err)
	}
	d, _ := q.Fermentation(e.ctx, r.FermentationID)
	if *d.Stats.OG != 1.060 { // manual OG beats the estimate from readings (1.050)
		t.Fatalf("OG = %v", *d.Stats.OG)
	}
	// A second OG replaces the first one's flag.
	second, _ := e.edit.AddManualReading(e.ctx, domain.ManualReading{
		FermentationID: r.FermentationID, TakenAt: start, GravitySG: 1.058, IsOG: true,
	})
	d, _ = q.Fermentation(e.ctx, r.FermentationID)
	flagged := 0
	for _, m := range d.Manual {
		if m.IsOG {
			flagged++
		}
	}
	if flagged != 1 || *d.Stats.OG != 1.058 || d.Manual[0].Method == "" {
		t.Fatalf("flagged=%d OG=%v manual=%+v", flagged, *d.Stats.OG, d.Manual)
	}
	// The override only applies when there's no manual OG.
	if _, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{OGOverride: ptr(f64(1.070))}); err != nil {
		t.Fatal(err)
	}
	if d, _ = q.Fermentation(e.ctx, r.FermentationID); *d.Stats.OG != 1.058 {
		t.Fatalf("manual OG should win over override: %v", *d.Stats.OG)
	}
	if err := e.edit.DeleteManualReading(e.ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	// The first OG flag was cleared when the second was added, so with the
	// second deleted the override (1.070) applies again.
	if d, _ = q.Fermentation(e.ctx, r.FermentationID); *d.Stats.OG != 1.070 {
		t.Fatalf("OG after delete = %v", *d.Stats.OG)
	}

	if _, err := e.edit.AddManualReading(e.ctx, domain.ManualReading{
		FermentationID: r.FermentationID, TakenAt: start.Add(time.Hour), GravitySG: 1.012, IsFG: true,
	}); err != nil {
		t.Fatal(err)
	}
	if d, _ = q.Fermentation(e.ctx, r.FermentationID); d.MeasuredFG == nil || *d.MeasuredFG != 1.012 {
		t.Fatalf("measured FG = %v", d.MeasuredFG)
	}
}

func ptr[T any](v T) *T { return &v }

func TestManualReadingValidation(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	ok := domain.ManualReading{FermentationID: r.FermentationID, TakenAt: e.clock.t, GravitySG: 1.05}
	bad := map[string]domain.ManualReading{
		"gravity too high": {FermentationID: ok.FermentationID, TakenAt: ok.TakenAt, GravitySG: 5},
		"gravity too low":  {FermentationID: ok.FermentationID, TakenAt: ok.TakenAt, GravitySG: 0.5},
		"bad method":       {FermentationID: ok.FermentationID, TakenAt: ok.TakenAt, GravitySG: 1.05, Method: "guess"},
		"og and fg":        {FermentationID: ok.FermentationID, TakenAt: ok.TakenAt, GravitySG: 1.05, IsOG: true, IsFG: true},
		"no time":          {FermentationID: ok.FermentationID, GravitySG: 1.05},
	}
	for name, m := range bad {
		if _, err := e.edit.AddManualReading(e.ctx, m); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	missing := ok
	missing.FermentationID = 999
	if _, err := e.edit.AddManualReading(e.ctx, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown fermentation: %v", err)
	}
}

func TestAnnotationsAndSeriesMarkers(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 4)
	if _, err := e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: e.clock.t, Kind: "party"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad kind: %v", err)
	}
	a, err := e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: e.clock.t, Kind: "Dry hop", Note: " Citra "})
	if err != nil || a.Note != "Citra" {
		t.Fatalf("%+v %v", a, err)
	}
	_, _ = e.edit.AddManualReading(e.ctx, domain.ManualReading{FermentationID: r.FermentationID, TakenAt: e.clock.t, GravitySG: 1.048, IsFG: true})

	q := domain.NewQueries(e.store, e.clock)
	d, _ := q.Fermentation(e.ctx, r.FermentationID)
	s := domain.BuildSeries(d.Readings, domain.DefaultPreferences(), false)
	s.AttachMarkers(d.Manual, d.Annotations, domain.DefaultPreferences())
	if len(s.Annotations) != 1 || s.Annotations[0].Note != "Dry hop: Citra" || len(s.Manual) != 1 || s.Manual[0].Kind != "FG" {
		t.Fatalf("%+v %+v", s.Annotations, s.Manual)
	}
	if err := e.edit.DeleteAnnotation(e.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.edit.DeleteAnnotation(e.ctx, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}

func TestSplitAndMergeCarryManualReadingsAndAnnotations(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 10)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	early, late := start.Add(15*time.Minute), start.Add(2*time.Hour)
	_, _ = e.edit.AddManualReading(e.ctx, domain.ManualReading{FermentationID: r.FermentationID, TakenAt: early, GravitySG: 1.05})
	_, _ = e.edit.AddManualReading(e.ctx, domain.ManualReading{FermentationID: r.FermentationID, TakenAt: late, GravitySG: 1.03})
	_, _ = e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: early, Kind: "Pitched yeast"})
	_, _ = e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: late, Kind: "Dry hop"})

	earlier, later, err := e.edit.Split(e.ctx, r.FermentationID, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	q := domain.NewQueries(e.store, e.clock)
	de, _ := q.Fermentation(e.ctx, earlier.ID)
	dl, _ := q.Fermentation(e.ctx, later.ID)
	if len(de.Manual) != 1 || len(de.Annotations) != 1 || de.Annotations[0].Kind != "Pitched yeast" ||
		len(dl.Manual) != 1 || len(dl.Annotations) != 1 || dl.Annotations[0].Kind != "Dry hop" {
		t.Fatalf("after split: earlier %d/%d later %d/%d", len(de.Manual), len(de.Annotations), len(dl.Manual), len(dl.Annotations))
	}
	merged, err := e.edit.Merge(e.ctx, later.ID, earlier.ID)
	if err != nil {
		t.Fatal(err)
	}
	dm, _ := q.Fermentation(e.ctx, merged.ID)
	if len(dm.Manual) != 2 || len(dm.Annotations) != 2 {
		t.Fatalf("after merge: %d/%d", len(dm.Manual), len(dm.Annotations))
	}
}

func TestRecipes(t *testing.T) {
	e := newEnv(t)
	q := domain.NewQueries(e.store, e.clock)
	if _, err := e.edit.CreateRecipe(e.ctx, domain.Recipe{Name: "  "}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := e.edit.CreateRecipe(e.ctx, domain.Recipe{Name: "x", TargetOG: f64(2)}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad og: %v", err)
	}
	rec, err := e.edit.CreateRecipe(e.ctx, domain.Recipe{Name: " Weizen ", Style: "Hefeweizen", TargetOG: f64(1.052), TargetFG: f64(1.012)})
	if err != nil || rec.Name != "Weizen" || rec.CreatedAt.IsZero() {
		t.Fatalf("%+v %v", rec, err)
	}

	r := e.ingestN(t, "A1", 2)
	if _, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{RecipeID: ptr(ptr(rec.ID))}); err != nil {
		t.Fatal(err)
	}
	d, _ := q.Fermentation(e.ctx, r.FermentationID)
	if d.Recipe == nil || d.Recipe.Name != "Weizen" || len(d.Recipes) != 1 {
		t.Fatalf("detail recipe: %+v", d.Recipe)
	}
	rs, _ := q.Recipes(e.ctx)
	if len(rs) != 1 || rs[0].Fermentations != 1 {
		t.Fatalf("recipes: %+v", rs)
	}
	rd, err := q.Recipe(e.ctx, rec.ID)
	if err != nil || len(rd.Fermentations) != 1 || rd.Fermentations[0].ID != r.FermentationID {
		t.Fatalf("recipe detail: %+v %v", rd, err)
	}

	rec.Name = "Weizen v2"
	if upd, err := e.edit.UpdateRecipe(e.ctx, rec); err != nil || upd.Name != "Weizen v2" {
		t.Fatalf("%+v %v", upd, err)
	}
	// Clearing the link and deleting the recipe keep the fermentation.
	if err := e.edit.DeleteRecipe(e.ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = q.Fermentation(e.ctx, r.FermentationID)
	if d.Recipe != nil || d.Fermentation.RecipeID != nil {
		t.Fatalf("recipe link survived deletion: %+v", d.Fermentation.RecipeID)
	}
	if _, err := q.Recipe(e.ctx, rec.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted recipe: %v", err)
	}
}

func TestUpdateRejectsMissingRecipe(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	_, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{RecipeID: ptr(ptr(int64(999)))})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	// Clearing the recipe is still allowed.
	if _, err := e.edit.Update(e.ctx, r.FermentationID, domain.FermentationPatch{RecipeID: ptr[*int64](nil)}); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAndDeviceChangesPublishEvents(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 1)
	e.events.events = nil

	prefs := domain.NewPreferenceService(e.store, e.events)
	p := domain.DefaultPreferences()
	p.Gravity = domain.DisplayPlato
	if err := prefs.Update(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	if len(e.events.events) != 1 || e.events.events[0].Type != domain.EventSettings {
		t.Fatalf("settings events: %+v", e.events.events)
	}

	e.events.events = nil
	name := "Renamed"
	if _, err := e.edit.UpdateDevice(e.ctx, r.DeviceID, domain.DevicePatch{DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	if len(e.events.events) != 1 || e.events.events[0].FermentationID != r.FermentationID {
		t.Fatalf("device events: %+v", e.events.events)
	}

	e.events.events = nil
	bad := domain.Preferences{}
	if err := prefs.Update(e.ctx, bad); err == nil || len(e.events.events) != 0 {
		t.Fatalf("invalid update must not publish: %v %+v", err, e.events.events)
	}
}

func TestAnnotationTypesAreUserManaged(t *testing.T) {
	e := newEnv(t)
	r := e.ingestN(t, "A1", 2)
	q := domain.NewQueries(e.store, e.clock)

	types, _ := q.AnnotationTypes(e.ctx)
	if len(types) != len(domain.DefaultAnnotationTypes) {
		t.Fatalf("defaults: %+v", types)
	}
	if _, err := e.edit.AddAnnotationType(e.ctx, "   "); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := e.edit.AddAnnotationType(e.ctx, strings.Repeat("x", 41)); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("long name: %v", err)
	}
	if _, err := e.edit.AddAnnotationType(e.ctx, "dry HOP"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}

	fruit, err := e.edit.AddAnnotationType(e.ctx, "  Added fruit ")
	if err != nil || fruit.Name != "Added fruit" {
		t.Fatalf("add: %+v %v", fruit, err)
	}
	if _, err := e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: e.clock.t, Kind: "Added fruit"}); err != nil {
		t.Fatalf("new type is usable: %v", err)
	}

	// Renaming relabels the logged event and notifies open pages.
	e.events.events = nil
	if err := e.edit.RenameAnnotationType(e.ctx, fruit.ID, "Fruit addition"); err != nil {
		t.Fatal(err)
	}
	d, _ := q.Fermentation(e.ctx, r.FermentationID)
	if len(d.Annotations) != 1 || d.Annotations[0].Kind != "Fruit addition" || len(e.events.events) == 0 {
		t.Fatalf("relabel: %+v events=%d", d.Annotations, len(e.events.events))
	}
	if _, err := e.edit.AddAnnotation(e.ctx, domain.Annotation{FermentationID: r.FermentationID, At: e.clock.t, Kind: "Added fruit"}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("old name must be gone: %v", err)
	}

	// Removing a type keeps logged events; the last type cannot be removed.
	if err := e.edit.DeleteAnnotationType(e.ctx, fruit.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = q.Fermentation(e.ctx, r.FermentationID)
	if len(d.Annotations) != 1 || d.Annotations[0].Kind != "Fruit addition" {
		t.Fatalf("events must survive: %+v", d.Annotations)
	}
	types, _ = q.AnnotationTypes(e.ctx)
	for _, ty := range types[:len(types)-1] {
		if err := e.edit.DeleteAnnotationType(e.ctx, ty.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.edit.DeleteAnnotationType(e.ctx, types[len(types)-1].ID); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("last type: %v", err)
	}
}
