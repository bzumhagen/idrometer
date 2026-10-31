package domain

import (
	"context"
	"errors"
	"time"
)

// Queries builds the read models the UI and API render.
type Queries struct {
	tx    TxManager
	clock Clock
}

func NewQueries(tx TxManager, clock Clock) *Queries { return &Queries{tx: tx, clock: clock} }

// DeviceStatus is one device with its current fermentation and newest reading.
type DeviceStatus struct {
	Device Device
	Active *Fermentation
	Latest *Reading
	// Spark is the last 48 h of gravity (SG, calibrated, nil gaps dropped)
	// and Rate the SG change per day, both for the active fermentation.
	Spark []float64
	Rate  *float64
}

func (q *Queries) Dashboard(ctx context.Context) ([]DeviceStatus, error) {
	var out []DeviceStatus
	err := q.tx.View(ctx, func(r Repos) error {
		out = out[:0]
		devs, err := r.Devices.List(ctx)
		if err != nil {
			return err
		}
		for _, d := range devs {
			st := DeviceStatus{Device: d}
			f, err := r.Fermentations.ActiveForDevice(ctx, d.ID)
			switch {
			case errors.Is(err, ErrNotFound):
			case err != nil:
				return err
			default:
				st.Active = &f
				rd, err := r.Readings.Latest(ctx, f.ID)
				if err == nil {
					cal, err := calibrationFor(ctx, r, d.ID)
					if err != nil {
						return err
					}
					rd.Gravity = effectiveGravity(rd, cal)
					st.Latest = &rd
					kept, err := keptReadings(ctx, r, f.ID, cal)
					if err != nil {
						return err
					}
					st.Rate = ComputeStats(kept, nil).RatePerDay
					for _, k := range kept {
						if k.Gravity > 0 && rd.ReceivedAt.Sub(k.ReceivedAt) <= sparkWindow {
							st.Spark = append(st.Spark, k.Gravity)
						}
					}
				} else if !errors.Is(err, ErrNotFound) {
					return err
				}
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

// FermentationSummary is a row in the fermentation list.
type FermentationSummary struct {
	Fermentation
	DeviceName  string
	FirstAt     time.Time
	LastAt      time.Time
	LatestSG    float64 // 0 = none
	LatestTempC float64
	HasReadings bool
	// Stats-derived figures (nil when there is not enough data).
	OG        *float64
	CurrentSG *float64
	ABV       *float64
}

// sparkWindow is how much history the dashboard sparkline shows.
const sparkWindow = 48 * time.Hour

// keptReadings loads a fermentation's non-excluded readings with calibrated gravity.
func keptReadings(ctx context.Context, r Repos, fermentationID int64, cal *Calibration) ([]Reading, error) {
	rs, err := r.Readings.List(ctx, fermentationID, false)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		rs[i].Gravity = effectiveGravity(rs[i], cal)
	}
	return rs, nil
}

func (q *Queries) Fermentations(ctx context.Context, fl FermentationFilter) ([]FermentationSummary, error) {
	var out []FermentationSummary
	err := q.tx.View(ctx, func(r Repos) error {
		var err error
		out, err = summaries(ctx, r, fl)
		return err
	})
	return out, err
}

func summaries(ctx context.Context, r Repos, fl FermentationFilter) ([]FermentationSummary, error) {
	fs, err := r.Fermentations.List(ctx, fl)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	cals := map[int64]*Calibration{}
	var out []FermentationSummary
	for _, f := range fs {
		if _, ok := names[f.DeviceID]; !ok {
			d, err := r.Devices.Get(ctx, f.DeviceID)
			if err != nil {
				return nil, err
			}
			names[f.DeviceID] = d.Name()
			if cals[f.DeviceID], err = calibrationFor(ctx, r, d.ID); err != nil {
				return nil, err
			}
		}
		s := FermentationSummary{Fermentation: f, DeviceName: names[f.DeviceID]}
		first, last, ok, err := r.Readings.Bounds(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			s.HasReadings, s.FirstAt, s.LastAt = true, first, last
			if rd, err := r.Readings.Latest(ctx, f.ID); err == nil {
				s.LatestSG, s.LatestTempC = effectiveGravity(rd, cals[f.DeviceID]), rd.TemperatureC
			} else if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			kept, err := keptReadings(ctx, r, f.ID, cals[f.DeviceID])
			if err != nil {
				return nil, err
			}
			manual, err := r.Manual.List(ctx, f.ID)
			if err != nil {
				return nil, err
			}
			st := ComputeStats(kept, ogFor(f, manual))
			s.OG, s.CurrentSG, s.ABV = st.OG, st.CurrentSG, st.ABV
		}
		out = append(out, s)
	}
	return out, nil
}

// FermentationDetail is everything the detail page and graph need.
type FermentationDetail struct {
	Fermentation  Fermentation
	Device        Device
	Calibration   *Calibration
	Recipe        *Recipe
	Recipes       []Recipe  // for the recipe picker
	Readings      []Reading // includes excluded ones, flagged; Gravity is calibrated
	Manual        []ManualReading
	Annotations   []Annotation
	Stats         Stats // over non-excluded readings
	MeasuredFG    *float64
	ExcludedCount int
}

// ogFor picks the original gravity for stats: a manual reading flagged OG,
// else the fermentation's override, else nil (estimated from readings).
func ogFor(f Fermentation, manual []ManualReading) *float64 {
	for i := len(manual) - 1; i >= 0; i-- {
		if manual[i].IsOG {
			v := manual[i].GravitySG
			return &v
		}
	}
	return f.OGOverride
}

func (q *Queries) Fermentation(ctx context.Context, id int64) (FermentationDetail, error) {
	var d FermentationDetail
	err := q.tx.View(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		dev, err := r.Devices.Get(ctx, f.DeviceID)
		if err != nil {
			return err
		}
		cal, err := calibrationFor(ctx, r, dev.ID)
		if err != nil {
			return err
		}
		rs, err := r.Readings.List(ctx, id, true)
		if err != nil {
			return err
		}
		kept := make([]Reading, 0, len(rs))
		for i := range rs {
			rs[i].Gravity = effectiveGravity(rs[i], cal)
			if rs[i].Excluded {
				d.ExcludedCount++
			} else {
				kept = append(kept, rs[i])
			}
		}
		if d.Manual, err = r.Manual.List(ctx, id); err != nil {
			return err
		}
		if d.Annotations, err = r.Annotations.List(ctx, id); err != nil {
			return err
		}
		if d.Recipes, err = r.Recipes.List(ctx); err != nil {
			return err
		}
		if f.RecipeID != nil {
			rec, err := r.Recipes.Get(ctx, *f.RecipeID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if err == nil {
				d.Recipe = &rec
			}
		}
		for i := len(d.Manual) - 1; i >= 0; i-- {
			if d.Manual[i].IsFG {
				v := d.Manual[i].GravitySG
				d.MeasuredFG = &v
				break
			}
		}
		d.Fermentation, d.Device, d.Calibration, d.Readings = f, dev, cal, rs
		d.Stats = ComputeStats(kept, ogFor(f, d.Manual))
		return nil
	})
	return d, err
}

func (q *Queries) Devices(ctx context.Context) ([]Device, error) {
	var out []Device
	err := q.tx.View(ctx, func(r Repos) (err error) { out, err = r.Devices.List(ctx); return })
	return out, err
}

// DeviceCalibrations returns each device's calibration, keyed by device id.
func (q *Queries) DeviceCalibrations(ctx context.Context) (map[int64]Calibration, error) {
	out := map[int64]Calibration{}
	err := q.tx.View(ctx, func(r Repos) error {
		devs, err := r.Devices.List(ctx)
		if err != nil {
			return err
		}
		for _, d := range devs {
			c, err := calibrationFor(ctx, r, d.ID)
			if err != nil {
				return err
			}
			if c != nil {
				out[d.ID] = *c
			}
		}
		return nil
	})
	return out, err
}

// DeviceCalibration returns the device's calibration, or nil.
func (q *Queries) DeviceCalibration(ctx context.Context, deviceID int64) (*Calibration, error) {
	var c *Calibration
	err := q.tx.View(ctx, func(r Repos) (err error) { c, err = calibrationFor(ctx, r, deviceID); return })
	return c, err
}

func (q *Queries) Preferences(ctx context.Context) (Preferences, error) {
	var p Preferences
	err := q.tx.View(ctx, func(r Repos) (err error) { p, err = loadPreferences(ctx, r); return })
	return p, err
}

// MergeCandidates returns the other non-deleted fermentations of the same
// device, for the merge picker.
func (q *Queries) MergeCandidates(ctx context.Context, id int64) ([]Fermentation, error) {
	var out []Fermentation
	err := q.tx.View(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		all, err := r.Fermentations.List(ctx, FermentationFilter{DeviceID: f.DeviceID})
		if err != nil {
			return err
		}
		for _, o := range all {
			if o.ID != id {
				out = append(out, o)
			}
		}
		return nil
	})
	return out, err
}

// RecipeSummary is a recipe with how many fermentations used it.
type RecipeSummary struct {
	Recipe
	Fermentations int
}

func (q *Queries) Recipes(ctx context.Context) ([]RecipeSummary, error) {
	var out []RecipeSummary
	err := q.tx.View(ctx, func(r Repos) error {
		rs, err := r.Recipes.List(ctx)
		if err != nil {
			return err
		}
		all, err := r.Fermentations.List(ctx, FermentationFilter{})
		if err != nil {
			return err
		}
		counts := map[int64]int{}
		for _, f := range all {
			if f.RecipeID != nil {
				counts[*f.RecipeID]++
			}
		}
		for _, x := range rs {
			out = append(out, RecipeSummary{Recipe: x, Fermentations: counts[x.ID]})
		}
		return nil
	})
	return out, err
}

// RecipeDetail is a recipe with the fermentations brewed from it.
type RecipeDetail struct {
	Recipe        Recipe
	Fermentations []FermentationSummary
}

func (q *Queries) Recipe(ctx context.Context, id int64) (RecipeDetail, error) {
	var d RecipeDetail
	err := q.tx.View(ctx, func(r Repos) error {
		rec, err := r.Recipes.Get(ctx, id)
		if err != nil {
			return err
		}
		all, err := summaries(ctx, r, FermentationFilter{})
		if err != nil {
			return err
		}
		d.Recipe = rec
		for _, s := range all {
			if s.RecipeID != nil && *s.RecipeID == id {
				d.Fermentations = append(d.Fermentations, s)
			}
		}
		return nil
	})
	return d, err
}

// EventTypes returns the event types available when logging an event.
func (q *Queries) AnnotationTypes(ctx context.Context) ([]AnnotationType, error) {
	var out []AnnotationType
	err := q.tx.View(ctx, func(r Repos) (err error) { out, err = r.AnnotationTypes.List(ctx); return })
	return out, err
}
