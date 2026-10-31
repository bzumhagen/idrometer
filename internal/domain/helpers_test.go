package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/store/memory"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type recorder struct{ events []domain.Event }

func (r *recorder) Publish(e domain.Event) { r.events = append(r.events, e) }

type env struct {
	ctx    context.Context
	store  *memory.Store
	clock  *fakeClock
	events *recorder
	ing    *domain.Ingestor
	edit   *domain.Editor
	sweep  *domain.Sweeper
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := memory.New()
	c := &fakeClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	ev := &recorder{}
	return &env{
		ctx: context.Background(), store: s, clock: c, events: ev,
		ing:   domain.NewIngestor(s, c, ev),
		edit:  domain.NewEditor(s, c, ev),
		sweep: domain.NewSweeper(s, c, ev, nil),
	}
}

func input(hw string) domain.ReadingInput {
	return domain.ReadingInput{
		Source: "ispindel", HardwareID: hw, Name: "iSpindel-" + hw,
		Angle: 40, TemperatureC: 19.5, BatteryV: 4.0, Gravity: 1.050, RSSI: -60, IntervalS: 900,
	}
}

// ingestN sends n readings 15 minutes apart and returns the last one.
func (e *env) ingestN(t *testing.T, hw string, n int) domain.Reading {
	t.Helper()
	var r domain.Reading
	for i := 0; i < n; i++ {
		var err error
		if r, err = e.ing.Ingest(e.ctx, input(hw)); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(15 * time.Minute)
	}
	return r
}

func (e *env) ferm(t *testing.T, id int64) domain.Fermentation {
	t.Helper()
	var f domain.Fermentation
	err := e.store.WithinTx(e.ctx, func(r domain.Repos) (err error) {
		f, err = r.Fermentations.Get(e.ctx, id)
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (e *env) list(t *testing.T, fl domain.FermentationFilter) []domain.Fermentation {
	t.Helper()
	var out []domain.Fermentation
	err := e.store.WithinTx(e.ctx, func(r domain.Repos) (err error) {
		out, err = r.Fermentations.List(e.ctx, fl)
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) count(t *testing.T, id int64) int {
	t.Helper()
	var n int
	_ = e.store.WithinTx(e.ctx, func(r domain.Repos) error {
		rs, err := r.Readings.List(e.ctx, id, true)
		n = len(rs)
		return err
	})
	return n
}
