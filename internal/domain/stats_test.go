package domain_test

import (
	"math"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func series(start time.Time, n int, step time.Duration, gravity func(i int) float64) []domain.Reading {
	rs := make([]domain.Reading, n)
	for i := range rs {
		rs[i] = domain.Reading{
			ReceivedAt: start.Add(time.Duration(i) * step), Gravity: gravity(i),
			TemperatureC: 18 + float64(i%3),
		}
	}
	return rs
}

func TestComputeStatsEmpty(t *testing.T) {
	s := domain.ComputeStats(nil, nil)
	if s.Readings != 0 || s.OG != nil || s.ABV != nil {
		t.Fatalf("%+v", s)
	}
}

func TestComputeStats(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// 4 days of hourly readings falling linearly 1.060 -> 1.012.
	rs := series(start, 96, time.Hour, func(i int) float64 { return 1.060 - 0.048*float64(i)/95 })
	s := domain.ComputeStats(rs, nil)

	if s.Readings != 96 || s.Duration != 95*time.Hour {
		t.Fatalf("count/duration: %+v", s)
	}
	if s.OG == nil || math.Abs(*s.OG-1.0599) > 0.0005 {
		t.Fatalf("OG = %v", s.OG)
	}
	// Current gravity is the latest reading, not a smoothed value.
	if s.CurrentSG == nil || *s.CurrentSG != 1.012 {
		t.Fatalf("current = %v", s.CurrentSG)
	}
	if s.ABV == nil || math.Abs(*s.ABV-6.29) > 0.1 {
		t.Fatalf("ABV = %v", s.ABV)
	}
	if s.Attenuation == nil || *s.Attenuation < 75 || *s.Attenuation > 82 {
		t.Fatalf("attenuation = %v", s.Attenuation)
	}
	// Falling 0.048 over 95h => about -0.0121 SG/day.
	if s.RatePerDay == nil || math.Abs(*s.RatePerDay-(-0.0121)) > 0.001 {
		t.Fatalf("rate = %v", s.RatePerDay)
	}
	if *s.TempMinC != 18 || *s.TempMaxC != 20 || math.Abs(*s.TempAvgC-19) > 0.05 {
		t.Fatalf("temps = %v %v %v", *s.TempMinC, *s.TempAvgC, *s.TempMaxC)
	}
}

func TestOGOverrideWins(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rs := series(start, 10, time.Hour, func(int) float64 { return 1.040 })
	og := 1.050
	s := domain.ComputeStats(rs, &og)
	if *s.OG != 1.050 || math.Abs(*s.ABV-1.3125) > 1e-6 {
		t.Fatalf("OG=%v ABV=%v", *s.OG, *s.ABV)
	}
}

func TestMissingGravityIsSkipped(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rs := series(start, 5, time.Hour, func(int) float64 { return 0 }) // device reported no gravity
	s := domain.ComputeStats(rs, nil)
	if s.OG != nil || s.CurrentSG != nil || s.ABV != nil || s.RatePerDay != nil || s.TempAvgC == nil {
		t.Fatalf("%+v", s)
	}
}

func TestRateNeedsEnoughSpan(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rs := series(start, 3, 10*time.Minute, func(i int) float64 { return 1.05 - float64(i)*0.001 })
	if s := domain.ComputeStats(rs, nil); s.RatePerDay != nil {
		t.Fatalf("rate over 20 minutes should be nil, got %v", *s.RatePerDay)
	}
}

func TestCurrentGravityIsLatestReadingWithGravity(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rs := series(start, 5, time.Hour, func(i int) float64 { return 1.050 - 0.002*float64(i) })
	rs[4].Gravity = 0 // newest reading carries no gravity
	s := domain.ComputeStats(rs, nil)
	if s.CurrentSG == nil || math.Abs(*s.CurrentSG-1.044) > 1e-9 {
		t.Fatalf("current = %v, want 1.044", s.CurrentSG)
	}
}
