package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ---- recipes ----

func (e *Editor) CreateRecipe(ctx context.Context, r Recipe) (Recipe, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.validate(); err != nil {
		return Recipe{}, err
	}
	r.CreatedAt = e.clock.Now()
	r.UpdatedAt = r.CreatedAt
	err := e.tx.WithinTx(ctx, func(rp Repos) error { return rp.Recipes.Create(ctx, &r) })
	return r, err
}

func (e *Editor) UpdateRecipe(ctx context.Context, r Recipe) (Recipe, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := r.validate(); err != nil {
		return Recipe{}, err
	}
	r.UpdatedAt = e.clock.Now()
	err := e.tx.WithinTx(ctx, func(rp Repos) error { return rp.Recipes.Update(ctx, r) })
	return r, err
}

// DeleteRecipe removes a recipe; fermentations that used it are kept, unlinked.
func (e *Editor) DeleteRecipe(ctx context.Context, id int64) error {
	return e.tx.WithinTx(ctx, func(rp Repos) error { return rp.Recipes.Delete(ctx, id) })
}

// ---- manual readings ----

// AddManualReading records a hand-entered gravity. Marking it as OG or FG
// clears that mark from the fermentation's other readings.
func (e *Editor) AddManualReading(ctx context.Context, m ManualReading) (ManualReading, error) {
	if m.Method == "" {
		m.Method = MethodHydrometer
	}
	if err := m.validate(); err != nil {
		return ManualReading{}, err
	}
	var f Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		var err error
		if f, err = r.Fermentations.Get(ctx, m.FermentationID); err != nil {
			return err
		}
		if m.IsOG || m.IsFG {
			existing, err := r.Manual.List(ctx, m.FermentationID)
			if err != nil {
				return err
			}
			for _, o := range existing {
				if (m.IsOG && o.IsOG) || (m.IsFG && o.IsFG) {
					o.IsOG, o.IsFG = o.IsOG && !m.IsOG, o.IsFG && !m.IsFG
					if err := r.Manual.Update(ctx, o); err != nil {
						return err
					}
				}
			}
		}
		return r.Manual.Create(ctx, &m)
	})
	if err == nil {
		e.notify(f)
	}
	return m, err
}

func (e *Editor) DeleteManualReading(ctx context.Context, id int64) error {
	var f Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		m, err := r.Manual.Get(ctx, id)
		if err != nil {
			return err
		}
		if f, err = r.Fermentations.Get(ctx, m.FermentationID); err != nil {
			return err
		}
		return r.Manual.Delete(ctx, id)
	})
	if err == nil {
		e.notify(f)
	}
	return err
}

// ---- annotations ----

func (e *Editor) AddAnnotation(ctx context.Context, a Annotation) (Annotation, error) {
	a.Note = strings.TrimSpace(a.Note)
	if err := a.validate(); err != nil {
		return Annotation{}, err
	}
	var f Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		var err error
		if f, err = r.Fermentations.Get(ctx, a.FermentationID); err != nil {
			return err
		}
		types, err := r.AnnotationTypes.List(ctx)
		if err != nil {
			return err
		}
		known := false
		for _, t := range types {
			known = known || t.Name == a.Kind
		}
		if !known {
			return fmt.Errorf("%w: unknown event type %q", ErrInvalid, a.Kind)
		}
		return r.Annotations.Create(ctx, &a)
	})
	if err == nil {
		e.notify(f)
	}
	return a, err
}

func (e *Editor) DeleteAnnotation(ctx context.Context, id int64) error {
	var f Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		a, err := r.Annotations.Get(ctx, id)
		if err != nil {
			return err
		}
		if f, err = r.Fermentations.Get(ctx, a.FermentationID); err != nil {
			return err
		}
		return r.Annotations.Delete(ctx, id)
	})
	if err == nil {
		e.notify(f)
	}
	return err
}

// ---- calibration ----

// SetCalibration replaces a device's calibration. Because gravity is computed
// when read, past readings are recalibrated too.
func (e *Editor) SetCalibration(ctx context.Context, c Calibration) error {
	c.UpdatedAt = e.clock.Now()
	if err := c.validate(); err != nil {
		return err
	}
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		if _, err := r.Devices.Get(ctx, c.DeviceID); err != nil {
			return err
		}
		return r.Calibrations.Set(ctx, c)
	})
	if err == nil {
		e.notifyDevice(ctx, c.DeviceID)
	}
	return err
}

// ClearCalibration returns a device to the gravity it reports itself.
func (e *Editor) ClearCalibration(ctx context.Context, deviceID int64) error {
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		if _, err := r.Devices.Get(ctx, deviceID); err != nil {
			return err
		}
		return r.Calibrations.Delete(ctx, deviceID)
	})
	if err == nil {
		e.notifyDevice(ctx, deviceID)
	}
	return err
}

// notifyDevice tells open pages that every fermentation of a device changed.
func (e *Editor) notifyDevice(ctx context.Context, deviceID int64) {
	var fs []Fermentation
	_ = e.tx.View(ctx, func(r Repos) (err error) {
		fs, err = r.Fermentations.List(ctx, FermentationFilter{DeviceID: deviceID})
		return
	})
	for _, f := range fs {
		e.notify(f)
	}
}

// ---- event types ----

func (e *Editor) AddAnnotationType(ctx context.Context, name string) (AnnotationType, error) {
	name, err := cleanAnnotationTypeName(name)
	if err != nil {
		return AnnotationType{}, err
	}
	t := AnnotationType{Name: name}
	err = e.tx.WithinTx(ctx, func(r Repos) error { return r.AnnotationTypes.Create(ctx, &t) })
	if errors.Is(err, ErrConflict) {
		return AnnotationType{}, fmt.Errorf("%w: there is already an event type named %q", ErrConflict, name)
	}
	return t, err
}

// RenameAnnotationType renames a type and relabels the events already logged with it.
func (e *Editor) RenameAnnotationType(ctx context.Context, id int64, name string) error {
	name, err := cleanAnnotationTypeName(name)
	if err != nil {
		return err
	}
	err = e.tx.WithinTx(ctx, func(r Repos) error { return r.AnnotationTypes.Rename(ctx, id, name) })
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: there is already an event type named %q", ErrConflict, name)
	}
	if err == nil {
		e.notifyAll(ctx)
	}
	return err
}

// DeleteAnnotationType removes a type from the picker. At least one must remain so
// events can still be added; events already logged keep their label.
func (e *Editor) DeleteAnnotationType(ctx context.Context, id int64) error {
	return e.tx.WithinTx(ctx, func(r Repos) error {
		types, err := r.AnnotationTypes.List(ctx)
		if err != nil {
			return err
		}
		if len(types) <= 1 {
			return fmt.Errorf("%w: keep at least one event type", ErrInvalid)
		}
		return r.AnnotationTypes.Delete(ctx, id)
	})
}

// notifyAll tells open pages that something spanning fermentations changed.
func (e *Editor) notifyAll(ctx context.Context) {
	var fs []Fermentation
	_ = e.tx.View(ctx, func(r Repos) (err error) {
		fs, err = r.Fermentations.List(ctx, FermentationFilter{})
		return
	})
	for _, f := range fs {
		e.notify(f)
	}
}
