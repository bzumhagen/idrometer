package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Ingestor records readings and assigns them to fermentations.
type Ingestor struct {
	tx    TxManager
	clock Clock
	pub   EventPublisher
}

func NewIngestor(tx TxManager, clock Clock, pub EventPublisher) *Ingestor {
	return &Ingestor{tx: tx, clock: clock, pub: pub}
}

// Ingest stores one reading. It registers unknown devices, starts a new
// fermentation when the device has no active one or the gap since its last
// reading exceeds the threshold, and publishes events after commit.
func (i *Ingestor) Ingest(ctx context.Context, in ReadingInput) (Reading, error) {
	if err := validateInput(in); err != nil {
		return Reading{}, err
	}
	now := i.clock.Now()
	var (
		out     Reading
		created bool
		closed  *Fermentation
	)
	err := i.tx.WithinTx(ctx, func(r Repos) error {
		created, closed = false, nil

		dev, err := r.Devices.GetBySource(ctx, in.Source, in.HardwareID)
		if errors.Is(err, ErrNotFound) {
			dev = Device{
				Source: in.Source, HardwareID: in.HardwareID,
				GravityUnit: GravityAuto, CreatedAt: now,
			}
			if err = r.Devices.Create(ctx, &dev); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		dev.ReportedName = in.Name
		dev.LastSeenAt = now
		dev.LastBatteryV = in.BatteryV
		dev.LastRSSI = in.RSSI
		if err := r.Devices.Update(ctx, dev); err != nil {
			return err
		}

		gravity, err := NormalizeGravity(in.Gravity, dev.GravityUnit)
		if err != nil {
			return err
		}
		gap, err := gapFor(ctx, r, dev)
		if err != nil {
			return err
		}

		active, err := r.Fermentations.ActiveForDevice(ctx, dev.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			created = true
		case err != nil:
			return err
		default:
			_, last, ok, err := r.Readings.Bounds(ctx, active.ID)
			if err != nil {
				return err
			}
			if !ok {
				last = active.StartedAt
			}
			if now.Sub(last) > gap {
				if err := closeFermentation(ctx, r, &active, last, CloseGap); err != nil {
					return err
				}
				c := active
				closed = &c
				created = true
			}
		}
		if created {
			active = Fermentation{
				DeviceID: dev.ID, Name: defaultName(dev, now),
				Status: StatusActive, StartedAt: now,
			}
			if err := r.Fermentations.Create(ctx, &active); err != nil {
				return err
			}
		}

		out = Reading{
			DeviceID: dev.ID, FermentationID: active.ID, ReceivedAt: now,
			Angle: in.Angle, TemperatureC: in.TemperatureC, BatteryV: in.BatteryV,
			GravityDevice: gravity, RSSI: in.RSSI, IntervalS: in.IntervalS,
			RawPayload: in.RawPayload,
		}
		if err := r.Readings.Insert(ctx, &out); err != nil {
			return err
		}
		cal, err := calibrationFor(ctx, r, dev.ID)
		if err != nil {
			return err
		}
		out.Gravity = effectiveGravity(out, cal)
		return nil
	})
	if err != nil {
		return Reading{}, err
	}
	if closed != nil {
		i.pub.Publish(Event{Type: EventFermentation, DeviceID: closed.DeviceID, FermentationID: closed.ID})
	}
	if created {
		i.pub.Publish(Event{Type: EventFermentation, DeviceID: out.DeviceID, FermentationID: out.FermentationID})
	}
	rd := out
	i.pub.Publish(Event{Type: EventReading, DeviceID: out.DeviceID, FermentationID: out.FermentationID, Reading: &rd})
	return out, nil
}

func defaultName(d Device, now time.Time) string {
	return fmt.Sprintf("%s · %s", d.Name(), now.In(time.Local).Format("2006-01-02"))
}

func closeFermentation(ctx context.Context, r Repos, f *Fermentation, endedAt time.Time, why CloseReason) error {
	f.Status = StatusCompleted
	f.EndedAt = &endedAt
	f.CloseReason = why
	return r.Fermentations.Update(ctx, *f)
}

func validateInput(in ReadingInput) error {
	if in.Source == "" || in.HardwareID == "" {
		return fmt.Errorf("%w: source and hardware id are required", ErrInvalid)
	}
	for name, v := range map[string]float64{
		"angle": in.Angle, "temperature": in.TemperatureC,
		"battery": in.BatteryV, "gravity": in.Gravity,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: %s is not a finite number", ErrInvalid, name)
		}
	}
	if in.TemperatureC < -50 || in.TemperatureC > 150 {
		return fmt.Errorf("%w: temperature %.1f°C out of range", ErrInvalid, in.TemperatureC)
	}
	return nil
}

// calibrationFor returns the device's calibration, or nil if it has none.
func calibrationFor(ctx context.Context, r Repos, deviceID int64) (*Calibration, error) {
	c, err := r.Calibrations.Get(ctx, deviceID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
