package domain

import (
	"math"
	"slices"
	"time"
)

// Stats summarises a fermentation's readings. Pointer fields are nil when
// there isn't enough data to compute them.
type Stats struct {
	Readings    int
	FirstAt     time.Time
	LastAt      time.Time
	Duration    time.Duration
	OG          *float64 // SG
	CurrentSG   *float64
	Attenuation *float64 // apparent, percent
	ABV         *float64 // percent
	RatePerDay  *float64 // SG change per day over the last 24h (negative while fermenting)
	TempMinC    *float64
	TempAvgC    *float64
	TempMaxC    *float64
}

const (
	ogWindow    = time.Hour
	rateWindow  = 24 * time.Hour
	minRateSpan = time.Hour
)

// ComputeStats works on non-excluded readings sorted by time. ogOverride, when
// set, replaces the OG estimated from the first hour of readings.
func ComputeStats(rs []Reading, ogOverride *float64) Stats {
	var s Stats
	s.Readings = len(rs)
	if len(rs) == 0 {
		return s
	}
	s.FirstAt, s.LastAt = rs[0].ReceivedAt, rs[len(rs)-1].ReceivedAt
	s.Duration = s.LastAt.Sub(s.FirstAt)

	lo, hi, sum := math.Inf(1), math.Inf(-1), 0.0
	for _, r := range rs {
		lo, hi, sum = math.Min(lo, r.TemperatureC), math.Max(hi, r.TemperatureC), sum+r.TemperatureC
	}
	avg := sum / float64(len(rs))
	s.TempMinC, s.TempMaxC, s.TempAvgC = &lo, &hi, &avg

	if ogOverride != nil {
		v := *ogOverride
		s.OG = &v
	} else if v, ok := medianGravity(rs, func(r Reading) bool { return r.ReceivedAt.Sub(s.FirstAt) <= ogWindow }); ok {
		s.OG = &v
	}
	// Current gravity is the newest reading that has one, not a smoothed value.
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Gravity > 0 {
			v := rs[i].Gravity
			s.CurrentSG = &v
			break
		}
	}
	if s.OG != nil && s.CurrentSG != nil {
		abv := (*s.OG - *s.CurrentSG) * 131.25
		s.ABV = &abv
		if *s.OG > 1 {
			att := (*s.OG - *s.CurrentSG) / (*s.OG - 1) * 100
			s.Attenuation = &att
		}
	}
	s.RatePerDay = gravitySlopePerDay(rs, s.LastAt)
	return s
}

func medianGravity(rs []Reading, in func(Reading) bool) (float64, bool) {
	var vs []float64
	for _, r := range rs {
		if r.Gravity > 0 && in(r) {
			vs = append(vs, r.Gravity)
		}
	}
	if len(vs) == 0 {
		return 0, false
	}
	slices.Sort(vs)
	if n := len(vs); n%2 == 0 {
		return (vs[n/2-1] + vs[n/2]) / 2, true
	}
	return vs[len(vs)/2], true
}

// gravitySlopePerDay is a least-squares slope over the last 24h of readings.
func gravitySlopePerDay(rs []Reading, last time.Time) *float64 {
	var xs, ys []float64
	for _, r := range rs {
		if r.Gravity > 0 && last.Sub(r.ReceivedAt) <= rateWindow {
			xs = append(xs, r.ReceivedAt.Sub(last).Hours()/24)
			ys = append(ys, r.Gravity)
		}
	}
	if len(xs) < 2 || time.Duration((xs[len(xs)-1]-xs[0])*24*float64(time.Hour)) < minRateSpan {
		return nil
	}
	var mx, my float64
	for i := range xs {
		mx, my = mx+xs[i], my+ys[i]
	}
	mx, my = mx/float64(len(xs)), my/float64(len(xs))
	var num, den float64
	for i := range xs {
		num += (xs[i] - mx) * (ys[i] - my)
		den += (xs[i] - mx) * (xs[i] - mx)
	}
	if den == 0 {
		return nil
	}
	slope := num / den
	return &slope
}
