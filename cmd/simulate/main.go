// Command simulate produces a synthetic iSpindel fermentation for development
// and demos. Readings arrive every 15 minutes like the real device, following
// a typical curve: a slow start, a fast middle, a long tail.
//
// Two modes:
//
//	simulate -seed-db idrometer.db -days 6    backfill history straight into a database (server stopped)
//	simulate -token T -start-day 6            post live readings to a running server, one per -interval
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/store/sqlite"
)

const step = 15 * time.Minute

// Gravity (SG) by day of a typical ale, measured from a real batch.
var curveDays = []float64{0, 1.94, 2.94, 3.94, 4.94, 5.94}
var curveSG = []float64{1.052, 1.048, 1.029, 1.021, 1.012, 1.006}

// pchip interpolates monotonically through the points, so the curve never
// overshoots or wiggles between them. Beyond the last point it holds steady.
func pchip(xs, ys []float64, x float64) float64 {
	n := len(xs)
	if x <= xs[0] {
		return ys[0]
	}
	if x >= xs[n-1] {
		return ys[n-1]
	}
	h := make([]float64, n-1)
	d := make([]float64, n-1)
	for i := range h {
		h[i] = xs[i+1] - xs[i]
		d[i] = (ys[i+1] - ys[i]) / h[i]
	}
	m := make([]float64, n)
	m[0], m[n-1] = d[0], d[n-2]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] <= 0 {
			continue
		}
		w1, w2 := 2*h[i]+h[i-1], h[i]+2*h[i-1]
		m[i] = (w1 + w2) / (w1/d[i-1] + w2/d[i])
	}
	i := 0
	for x > xs[i+1] {
		i++
	}
	t := (x - xs[i]) / h[i]
	t2, t3 := t*t, t*t*t
	return (2*t3-3*t2+1)*ys[i] + (t3-2*t2+t)*h[i]*m[i] + (-2*t3+3*t2)*ys[i+1] + (t3-t2)*h[i]*m[i+1]
}

// reading returns the device's report at a given day of the fermentation.
func reading(day, og float64, rng *rand.Rand, hw int, name string) domain.ReadingInput {
	// Scale the sample curve so any OG keeps the same shape.
	sg := 1 + (pchip(curveDays, curveSG, day)-1)*(og-1)/(curveSG[0]-1)
	sg += rng.NormFloat64() * 0.0004
	z := (day - 3) / 1.3
	heat := 1.8 * math.Exp(-z*z) // yeast warms the wort mid-fermentation
	temp := 18.5 + heat + 0.8*math.Sin(2*math.Pi*day) + rng.NormFloat64()*0.1
	return domain.ReadingInput{
		Source: "ispindel", HardwareID: fmt.Sprint(hw), Name: name,
		Angle:        60 - (sg-1)*600 + rng.NormFloat64()*0.05,
		TemperatureC: math.Round(temp*100) / 100,
		BatteryV:     math.Round((4.12-0.015*day)*1000) / 1000,
		Gravity:      math.Round(sg*10000) / 10000,
		RSSI:         -62 - rng.Intn(8),
		IntervalS:    int(step.Seconds()),
	}
}

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func seed(path string, days, og float64, hw int, name string) error {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		return err
	}
	defer store.Close()
	clk := &clock{}
	ing := domain.NewIngestor(store, clk, domain.NopPublisher{})
	rng := rand.New(rand.NewSource(1))
	end := time.Now().UTC().Truncate(time.Millisecond)
	n := 0
	for t := end.Add(-time.Duration(days * 24 * float64(time.Hour))); !t.After(end); t = t.Add(step) {
		clk.t = t.Add(time.Duration(rng.Intn(41)-20) * time.Second) // devices drift a little
		day := clk.t.Sub(end.Add(-time.Duration(days*24*float64(time.Hour)))).Hours() / 24
		if _, err := ing.Ingest(ctx, reading(day, og, rng, hw, name)); err != nil {
			return err
		}
		n++
	}
	fmt.Printf("seeded %d readings over %.1f days\n", n, days)
	return nil
}

func live(url, token string, startDay, og float64, count int, interval time.Duration, hw int, name string) error {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < count; i++ {
		day := startDay + float64(i)*step.Hours()/24
		in := reading(day, og, rng, hw, name)
		body, _ := json.Marshal(map[string]any{
			"name": in.Name, "ID": hw, "token": token, "angle": in.Angle, "temperature": in.TemperatureC,
			"temp_units": "C", "battery": in.BatteryV, "gravity": in.Gravity, "interval": in.IntervalS, "RSSI": in.RSSI,
		})
		resp, err := http.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("server replied %s", resp.Status)
		}
		time.Sleep(interval)
	}
	fmt.Printf("sent %d readings\n", count)
	return nil
}

func main() {
	var (
		seedDB   = flag.String("seed-db", "", "backfill this SQLite database with history (server must be stopped)")
		days     = flag.Float64("days", 6, "days of history to seed")
		url      = flag.String("url", "http://localhost:8080/api/v1/ingest/ispindel", "ingest URL (live mode)")
		token    = flag.String("token", os.Getenv("IDROMETER_INGEST_TOKEN"), "ingest token (live mode)")
		startDay = flag.Float64("start-day", 0, "day of the fermentation the first live reading represents")
		count    = flag.Int("count", 200, "live readings to send; each advances the curve by 15 minutes")
		interval = flag.Duration("interval", 100*time.Millisecond, "real delay between live readings")
		id       = flag.Int("id", 424242, "device ID")
		name     = flag.String("name", "iSpindel-sim", "device name")
		og       = flag.Float64("og", 1.052, "original gravity")
	)
	flag.Parse()
	var err error
	switch {
	case *seedDB != "":
		err = seed(*seedDB, *days, *og, *id, *name)
	case *token == "":
		fmt.Fprintln(os.Stderr, "simulate: give -seed-db, or -token / IDROMETER_INGEST_TOKEN for live mode")
		os.Exit(2)
	default:
		err = live(*url, *token, *startDay, *og, *count, *interval, *id, *name)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "simulate:", err)
		os.Exit(1)
	}
}
