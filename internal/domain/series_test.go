package domain_test

import (
	"math"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func TestBuildSeries(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rs := []domain.Reading{
		{ReceivedAt: t0, Gravity: 1.048, TemperatureC: 20, BatteryV: 4.1, RSSI: -60},
		{ReceivedAt: t0.Add(time.Hour), Gravity: 0, TemperatureC: 21, BatteryV: 4.0, RSSI: -61, Excluded: true},
		{ReceivedAt: t0.Add(2 * time.Hour), Gravity: 1.040, TemperatureC: 22, BatteryV: 4.0, RSSI: -62},
	}
	prefs := domain.Preferences{Gravity: domain.DisplayPlato, Temp: domain.TempF, Gap: 72 * time.Hour}

	s := domain.BuildSeries(rs, prefs, false)
	if len(s.T) != 2 || s.Excluded != nil || s.GravityUnit != "°P" || s.TempUnit != "°F" {
		t.Fatalf("%+v", s)
	}
	if math.Abs(s.Temp[0]-68) > 1e-9 || math.Abs(*s.Gravity[0]-12) > 0.1 {
		t.Fatalf("conversion: temp=%v gravity=%v", s.Temp[0], *s.Gravity[0])
	}

	all := domain.BuildSeries(rs, domain.DefaultPreferences(), true)
	if len(all.T) != 3 || !all.Excluded[1] || all.Excluded[0] || all.Gravity[1] != nil || *all.Gravity[0] != 1.048 {
		t.Fatalf("%+v", all)
	}
	if empty := domain.BuildSeries(nil, prefs, false); empty.T == nil || len(empty.T) != 0 {
		t.Fatal("empty series must serialise as [] not null")
	}
}
