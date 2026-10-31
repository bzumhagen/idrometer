package web

import (
	"fmt"
	"strings"
	"testing"
)

func TestSparkFlatNoiseStaysFlat(t *testing.T) {
	vals := []float64{1.0100, 1.0103, 1.0097, 1.0102, 1.0098, 1.0101}
	out := string(spark(vals))
	pts := out[strings.Index(out, `points="`)+8:]
	pts = pts[:strings.Index(pts, `"`)]
	lo, hi := 100.0, -100.0
	for _, p := range strings.Fields(pts) {
		var x, y float64
		if _, err := fmt.Sscanf(p, "%f,%f", &x, &y); err != nil {
			t.Fatal(err)
		}
		lo, hi = min(lo, y), max(hi, y)
	}
	if hi-lo > 12 { // of a 36-unit plot height
		t.Fatalf("noise fills too much of the sparkline: %.1f", hi-lo)
	}
}
