package domain

import (
	"context"
	"log/slog"
	"time"
)

// Sweeper closes active fermentations whose device has gone silent for longer
// than the gap, so the UI is accurate even when nothing new arrives.
type Sweeper struct {
	tx    TxManager
	clock Clock
	pub   EventPublisher
	log   *slog.Logger
}

func NewSweeper(tx TxManager, clock Clock, pub EventPublisher, log *slog.Logger) *Sweeper {
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{tx: tx, clock: clock, pub: pub, log: log}
}

// SweepOnce closes every stale active fermentation and returns how many.
func (s *Sweeper) SweepOnce(ctx context.Context) (int, error) {
	now := s.clock.Now()
	var closed []Fermentation
	err := s.tx.WithinTx(ctx, func(r Repos) error {
		closed = closed[:0]
		active, err := r.Fermentations.ListActive(ctx)
		if err != nil {
			return err
		}
		for _, f := range active {
			dev, err := r.Devices.Get(ctx, f.DeviceID)
			if err != nil {
				return err
			}
			gap, err := gapFor(ctx, r, dev)
			if err != nil {
				return err
			}
			_, last, ok, err := r.Readings.Bounds(ctx, f.ID)
			if err != nil {
				return err
			}
			if !ok {
				last = f.StartedAt
			}
			if now.Sub(last) <= gap {
				continue
			}
			if err := closeFermentation(ctx, r, &f, last, CloseGap); err != nil {
				return err
			}
			closed = append(closed, f)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, f := range closed {
		s.log.Info("closed silent fermentation", "id", f.ID, "device_id", f.DeviceID)
		s.pub.Publish(Event{Type: EventFermentation, DeviceID: f.DeviceID, FermentationID: f.ID})
	}
	return len(closed), nil
}

// Run sweeps every interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
