package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Editor holds the user-driven operations on fermentations.
// Authorization is the caller's concern (the API/web layer).
type Editor struct {
	tx    TxManager
	clock Clock
	pub   EventPublisher
}

func NewEditor(tx TxManager, clock Clock, pub EventPublisher) *Editor {
	return &Editor{tx: tx, clock: clock, pub: pub}
}

func (e *Editor) notify(f Fermentation) {
	e.pub.Publish(Event{Type: EventFermentation, DeviceID: f.DeviceID, FermentationID: f.ID})
}

// FermentationPatch holds optional edits; nil fields are left unchanged.
type FermentationPatch struct {
	Name       *string
	Notes      *string
	RecipeID   **int64 // pointer-to-pointer so the recipe can be cleared
	OGOverride **float64
}

func (e *Editor) Update(ctx context.Context, id int64, p FermentationPatch) (Fermentation, error) {
	var out Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		if p.Name != nil {
			name := strings.TrimSpace(*p.Name)
			if name == "" {
				return fmt.Errorf("%w: name must not be empty", ErrInvalid)
			}
			f.Name = name
		}
		if p.Notes != nil {
			f.Notes = *p.Notes
		}
		if p.RecipeID != nil {
			if id := *p.RecipeID; id != nil {
				if _, err := r.Recipes.Get(ctx, *id); errors.Is(err, ErrNotFound) {
					return fmt.Errorf("%w: recipe not found", ErrInvalid)
				} else if err != nil {
					return err
				}
			}
			f.RecipeID = *p.RecipeID
		}
		if p.OGOverride != nil {
			f.OGOverride = *p.OGOverride
		}
		out = f
		return r.Fermentations.Update(ctx, f)
	})
	if err == nil {
		e.notify(out)
	}
	return out, err
}

// Close marks an active fermentation completed. The next reading from the
// device starts a new fermentation.
func (e *Editor) Close(ctx context.Context, id int64) (Fermentation, error) {
	var out Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		if f.DeletedAt != nil || f.Status != StatusActive {
			return fmt.Errorf("%w: fermentation %d is not active", ErrConflict, id)
		}
		end := e.clock.Now()
		if _, last, ok, err := r.Readings.Bounds(ctx, id); err != nil {
			return err
		} else if ok {
			end = last
		}
		if err := closeFermentation(ctx, r, &f, end, CloseManual); err != nil {
			return err
		}
		out = f
		return nil
	})
	if err == nil {
		e.notify(out)
	}
	return out, err
}

// Reopen makes a completed fermentation active again. It fails with
// ErrConflict if the device already has another active fermentation (merge
// them instead).
func (e *Editor) Reopen(ctx context.Context, id int64) (Fermentation, error) {
	var out Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		if f.DeletedAt != nil || f.Status != StatusCompleted {
			return fmt.Errorf("%w: fermentation %d is not completed", ErrConflict, id)
		}
		if cur, err := r.Fermentations.ActiveForDevice(ctx, f.DeviceID); err == nil {
			return fmt.Errorf("%w: this device is already recording %q; complete it first, or merge the two", ErrConflict, cur.Name)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		f.Status, f.EndedAt, f.CloseReason = StatusActive, nil, ""
		if err := r.Fermentations.Update(ctx, f); err != nil {
			return err
		}
		out = f
		return nil
	})
	if err == nil {
		e.notify(out)
	}
	return out, err
}

// Delete soft-deletes a fermentation; its readings are kept.
func (e *Editor) Delete(ctx context.Context, id int64) error {
	return e.setDeleted(ctx, id, true)
}

// Restore undoes Delete. A previously active fermentation comes back as
// completed so it can't collide with the device's current one.
func (e *Editor) Restore(ctx context.Context, id int64) error {
	return e.setDeleted(ctx, id, false)
}

func (e *Editor) setDeleted(ctx context.Context, id int64, del bool) error {
	var out Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		if del {
			now := e.clock.Now()
			f.DeletedAt = &now
		} else {
			f.DeletedAt = nil
			if f.Status == StatusActive {
				end := e.clock.Now()
				if _, last, ok, err := r.Readings.Bounds(ctx, id); err != nil {
					return err
				} else if ok {
					end = last
				}
				if err := closeFermentation(ctx, r, &f, end, CloseManual); err != nil {
					return err
				}
				out = f
				return nil
			}
		}
		out = f
		return r.Fermentations.Update(ctx, f)
	})
	if err == nil {
		e.notify(out)
	}
	return err
}

// Split moves readings at or after `at` into a new fermentation. The earlier
// part is completed; the later part keeps the original's status.
func (e *Editor) Split(ctx context.Context, id int64, at time.Time) (earlier, later Fermentation, err error) {
	err = e.tx.WithinTx(ctx, func(r Repos) error {
		f, err := r.Fermentations.Get(ctx, id)
		if err != nil {
			return err
		}
		if f.DeletedAt != nil {
			return fmt.Errorf("%w: fermentation %d is deleted", ErrConflict, id)
		}
		first, last, ok, err := r.Readings.Bounds(ctx, id)
		if err != nil {
			return err
		}
		if !ok || !at.After(first) || at.After(last) {
			return fmt.Errorf("%w: split time must fall after the first and no later than the last reading", ErrInvalid)
		}
		// Which readings land in the later half decides its start.
		laterStatus, laterEnd := f.Status, f.EndedAt
		laterReason := f.CloseReason

		// Free the "active" slot before creating the later part.
		prevEnd, laterStart, err := splitBounds(ctx, r, id, at)
		if err != nil {
			return err
		}
		if err := closeFermentation(ctx, r, &f, prevEnd, CloseSplit); err != nil {
			return err
		}
		n := Fermentation{
			DeviceID: f.DeviceID, RecipeID: f.RecipeID, Name: f.Name + " (2)",
			Status: laterStatus, StartedAt: laterStart, EndedAt: laterEnd, CloseReason: laterReason,
			Notes: f.Notes,
		}
		if err := r.Fermentations.Create(ctx, &n); err != nil {
			return err
		}
		if err := r.Readings.MoveSince(ctx, id, n.ID, at); err != nil {
			return err
		}
		if err := r.Manual.MoveSince(ctx, id, n.ID, at); err != nil {
			return err
		}
		if err := r.Annotations.MoveSince(ctx, id, n.ID, at); err != nil {
			return err
		}
		earlier, later = f, n
		return nil
	})
	if err == nil {
		e.notify(earlier)
		e.notify(later)
	}
	return earlier, later, err
}

// splitBounds returns the time of the last reading before t and of the first
// reading at or after t.
func splitBounds(ctx context.Context, r Repos, id int64, t time.Time) (lastBefore, firstFrom time.Time, err error) {
	rs, err := r.Readings.List(ctx, id, true)
	if err != nil {
		return
	}
	for _, x := range rs {
		if x.ReceivedAt.Before(t) {
			if x.ReceivedAt.After(lastBefore) {
				lastBefore = x.ReceivedAt
			}
		} else if firstFrom.IsZero() || x.ReceivedAt.Before(firstFrom) {
			firstFrom = x.ReceivedAt
		}
	}
	return
}

// Merge moves all readings of src into dst (same device, no overlapping time
// ranges) and soft-deletes src. The result is active if either was.
func (e *Editor) Merge(ctx context.Context, srcID, dstID int64) (Fermentation, error) {
	if srcID == dstID {
		return Fermentation{}, fmt.Errorf("%w: cannot merge a fermentation into itself", ErrInvalid)
	}
	var out Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		src, err := r.Fermentations.Get(ctx, srcID)
		if err != nil {
			return err
		}
		dst, err := r.Fermentations.Get(ctx, dstID)
		if err != nil {
			return err
		}
		if src.DeletedAt != nil || dst.DeletedAt != nil {
			return fmt.Errorf("%w: cannot merge a deleted fermentation", ErrConflict)
		}
		if src.DeviceID != dst.DeviceID {
			return fmt.Errorf("%w: fermentations belong to different devices", ErrInvalid)
		}
		sf, sl, sok, err := r.Readings.Bounds(ctx, srcID)
		if err != nil {
			return err
		}
		df, dl, dok, err := r.Readings.Bounds(ctx, dstID)
		if err != nil {
			return err
		}
		if sok && dok && !sl.Before(df) && !dl.Before(sf) {
			return fmt.Errorf("%w: reading time ranges overlap", ErrConflict)
		}

		now := e.clock.Now()
		activeResult := src.Status == StatusActive || dst.Status == StatusActive
		// Soft-delete src first so the one-active-per-device rule isn't hit.
		src.DeletedAt = &now
		src.Status, src.EndedAt, src.CloseReason = StatusCompleted, &now, CloseManual
		if err := r.Fermentations.Update(ctx, src); err != nil {
			return err
		}
		if err := r.Readings.MoveAll(ctx, srcID, dstID); err != nil {
			return err
		}
		if err := r.Manual.MoveAll(ctx, srcID, dstID); err != nil {
			return err
		}
		if err := r.Annotations.MoveAll(ctx, srcID, dstID); err != nil {
			return err
		}
		if src.StartedAt.Before(dst.StartedAt) {
			dst.StartedAt = src.StartedAt
		}
		if activeResult {
			dst.Status, dst.EndedAt, dst.CloseReason = StatusActive, nil, ""
		} else {
			end := dst.StartedAt
			if sok && sl.After(end) {
				end = sl
			}
			if dok && dl.After(end) {
				end = dl
			}
			dst.EndedAt = &end
		}
		out = dst
		return r.Fermentations.Update(ctx, dst)
	})
	if err == nil {
		e.notify(out)
	}
	return out, err
}

// SetExcluded flags (or unflags) readings in [from, to] of a fermentation.
func (e *Editor) SetExcluded(ctx context.Context, id int64, from, to time.Time, excluded bool) (int64, error) {
	if to.Before(from) {
		return 0, fmt.Errorf("%w: range end is before start", ErrInvalid)
	}
	var n int64
	var f Fermentation
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		var err error
		if f, err = r.Fermentations.Get(ctx, id); err != nil {
			return err
		}
		n, err = r.Readings.SetExcluded(ctx, id, from, to, excluded)
		return err
	})
	if err == nil {
		e.notify(f)
	}
	return n, err
}

// DevicePatch holds optional device edits; nil fields are unchanged.
type DevicePatch struct {
	DisplayName  *string
	GapThreshold **time.Duration // pointer-to-pointer so the override can be cleared
	GravityUnit  *GravityUnit
}

func (e *Editor) UpdateDevice(ctx context.Context, id int64, p DevicePatch) (Device, error) {
	var out Device
	err := e.tx.WithinTx(ctx, func(r Repos) error {
		d, err := r.Devices.Get(ctx, id)
		if err != nil {
			return err
		}
		if p.DisplayName != nil {
			d.DisplayName = strings.TrimSpace(*p.DisplayName)
		}
		if p.GapThreshold != nil {
			if g := *p.GapThreshold; g != nil && (*g < time.Hour || *g > 365*24*time.Hour) {
				return fmt.Errorf("%w: gap must be between 1 hour and 365 days", ErrInvalid)
			}
			d.GapThreshold = *p.GapThreshold
		}
		if p.GravityUnit != nil {
			if !p.GravityUnit.Valid() {
				return fmt.Errorf("%w: gravity unit %q", ErrInvalid, *p.GravityUnit)
			}
			d.GravityUnit = *p.GravityUnit
		}
		out = d
		return r.Devices.Update(ctx, d)
	})
	if err == nil {
		e.notifyDevice(ctx, id)
	}
	return out, err
}
