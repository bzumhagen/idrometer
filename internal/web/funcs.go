package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// funcs are the helpers templates can call.
func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		// asset returns a static file URL fingerprinted by content, so browsers
		// can cache it for a long time yet pick up changes after an upgrade.
		"asset": s.assetURL,
		// gravity renders a stored SG value in the user's unit.
		"gravity": func(p domain.Preferences, sg float64) string { return formatGravity(p, sg) },
		"temp": func(p domain.Preferences, c float64) string {
			return formatTemp(p, c)
		},
		// time renders a <time> element that script localises to the browser's zone.
		"time": func(t time.Time) template.HTML {
			if t.IsZero() {
				return "–"
			}
			return template.HTML(`<time datetime="` + template.HTMLEscapeString(t.UTC().Format(time.RFC3339)) + `">` +
				template.HTMLEscapeString(t.UTC().Format("2006-01-02 15:04")) + ` UTC</time>`)
		},
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return "never"
			}
			d := s.Clock.Now().Sub(t)
			switch {
			case d < time.Minute:
				return "just now"
			case d < time.Hour:
				return pluralise(int(d.Minutes()), "min") + " ago"
			case d < 48*time.Hour:
				return pluralise(int(d.Hours()), "h") + " ago"
			}
			return pluralise(int(d.Hours()/24), "day") + " ago"
		},
		"duration": formatDuration,
		// gravityInput prefills a gravity field in the user's display unit.
		"gravityInput": func(p domain.Preferences, sg *float64) string {
			if sg == nil {
				return ""
			}
			return strconv.FormatFloat(p.ConvertGravity(*sg), 'f', p.GravityDecimals(), 64)
		},
		"gravityPlaceholder": func(p domain.Preferences) string {
			if p.Gravity == domain.DisplayPlato {
				return "12.5"
			}
			return "1.050"
		},
		"recipeID": func(f domain.Fermentation) int64 {
			if f.RecipeID == nil {
				return 0
			}
			return *f.RecipeID
		},
		"coef": func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) },
		"methodLabel": func(m domain.MeasureMethod) string {
			switch m {
			case domain.MethodRefractometer:
				return "Refractometer"
			case domain.MethodHydrometer:
				return "Hydrometer"
			}
			return "Other"
		},
		"tempInput": func(p domain.Preferences, c *float64) string {
			if c == nil {
				return ""
			}
			return strconv.FormatFloat(p.ConvertTemp(*c), 'f', 1, 64)
		},
		"derefFloat": func(p *float64) float64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"gapHours": func(d *time.Duration) string {
			if d == nil {
				return ""
			}
			return trimFloat(d.Hours())
		},
		"globalGapHours": func(p domain.Preferences) string { return trimFloat(p.Gap.Hours()) },
		"lowBattery":     func(v float64) bool { return v > 0 && v < 3.6 },
		"hint":           hint,
		"spark":          spark,
		"stale":          s.stale,
		"rssiBars":       rssiBars,
		"trend": func(p domain.Preferences, rate *float64, cur float64) string {
			v, _ := trendText(p, domain.Stats{RatePerDay: rate, CurrentSG: &cur})
			return v
		},
		"fmtAngle": func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
	}
}

// hint renders a "?" button that opens a short explanatory bubble. It uses the
// native popover attribute, so it works without JavaScript and on touch screens.
// id must be unique on the page.
func hint(id, text string) template.HTML {
	id = template.HTMLEscapeString(id)
	return template.HTML(`<button type="button" class="hint-btn" popovertarget="hint-` + id + `" aria-label="What is this?">?</button>` +
		`<span id="hint-` + id + `" popover class="hint-pop">` + template.HTMLEscapeString(text) + `</span>`)
}

// spark draws values as a small inline SVG line, scaled to fit.
// minSparkSpan is the smallest vertical range (about 1 °P) a sparkline draws.
const minSparkSpan = 0.004

func spark(vals []float64) template.HTML {
	if len(vals) < 2 {
		return ""
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if hi-lo < minSparkSpan { // flat: avoid magnifying noise
		mid := (hi + lo) / 2
		lo, hi = mid-minSparkSpan/2, mid+minSparkSpan/2
	}
	var b strings.Builder
	b.WriteString(`<svg class="spark" viewBox="0 0 200 40" preserveAspectRatio="none" role="img" aria-label="Gravity, last 48 hours"><polyline fill="none" points="`)
	for i, v := range vals {
		x := float64(i) / float64(len(vals)-1) * 200
		y := 38 - (v-lo)/(hi-lo)*36
		fmt.Fprintf(&b, "%.1f,%.1f ", x, y)
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}

// stale reports a device that has gone quiet: no report for over twice its
// usual interval (at least an hour).
func (s *Server) stale(d domain.Device, latest *domain.Reading) bool {
	if d.LastSeenAt.IsZero() {
		return true
	}
	limit := time.Hour
	if latest != nil && latest.IntervalS > 0 {
		if l := 2 * time.Duration(latest.IntervalS) * time.Second; l > limit {
			limit = l
		}
	}
	return s.Clock.Now().Sub(d.LastSeenAt) > limit
}

// rssiBars maps dBm to 0–4 signal bars.
func rssiBars(dbm int) int {
	switch {
	case dbm == 0:
		return 0
	case dbm >= -55:
		return 4
	case dbm >= -67:
		return 3
	case dbm >= -75:
		return 2
	}
	return 1
}

// fingerprintAssets maps each embedded static file to its cache-busting URL.
func fingerprintAssets() map[string]string {
	urls := map[string]string{}
	entries, _ := fs.ReadDir(staticFS, "static")
	for _, e := range entries {
		b, err := fs.ReadFile(staticFS, "static/"+e.Name())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		urls[e.Name()] = "/static/" + e.Name() + "?v=" + hex.EncodeToString(sum[:])[:10]
	}
	return urls
}

func (s *Server) assetURL(name string) string {
	if u, ok := s.assets[name]; ok {
		return u
	}
	return "/static/" + name
}
