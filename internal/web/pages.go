package web

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	st, err := s.Queries.Dashboard(r.Context())
	if err != nil {
		return "", err
	}
	p.Title, p.Nav, p.Data = "Dashboard", "dashboard", st
	return "dashboard", nil
}

// devicesPartial renders only the device cards, for live refresh.
func (s *Server) devicesPartial(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	st, err := s.Queries.Dashboard(r.Context())
	if err != nil {
		return "", err
	}
	p.Data = st
	var b bytes.Buffer
	if err := s.pages["dashboard"].ExecuteTemplate(&b, "devices", p); err != nil {
		return "", err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = b.WriteTo(w)
	return "", nil
}

type listData struct {
	Filter      string
	Items       []domain.FermentationSummary
	MultiDevice bool
}

func (s *Server) fermentationList(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	filter := r.URL.Query().Get("status")
	var fl domain.FermentationFilter
	switch filter {
	case "active":
		fl.Status = domain.StatusActive
	case "completed":
		fl.Status = domain.StatusCompleted
	case "deleted":
		fl.OnlyDeleted = true
	default:
		filter = ""
	}
	items, err := s.Queries.Fermentations(r.Context(), fl)
	if err != nil {
		return "", err
	}
	devices := map[string]bool{}
	for _, it := range items {
		devices[it.DeviceName] = true
	}
	p.Title, p.Nav, p.Data = "Fermentations", "fermentations", listData{Filter: filter, Items: items, MultiDevice: len(devices) > 1}
	return "fermentations", nil
}

// statCard is one figure. Sub is a short qualifier shown beneath it; Help is
// the explanation behind the "?" hint.
type statCard struct{ ID, Label, Value, Sub, Help string }

// statTiers splits the figures into the headline row and the compact detail row.
type statTiers struct{ Hero, Detail []statCard }

// logEntry is one line of the merged measurements-and-events log.
type logEntry struct {
	At        time.Time
	Kind      string // "Measurement" or the event type label
	Value     string
	Note      string
	DeleteURL string
	Confirm   string
}

type fermentationData struct {
	EventTypes []domain.AnnotationType
	D          domain.FermentationDetail
	Stats      statTiers
	Log        []logEntry
	Candidates []domain.Fermentation
	LastAt     time.Time
}

func (s *Server) fermentationPage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	id, err := pathID(r)
	if err != nil {
		return "", err
	}
	d, err := s.Queries.Fermentation(r.Context(), id)
	if err != nil {
		return "", err
	}
	cands, err := s.Queries.MergeCandidates(r.Context(), id)
	if err != nil {
		return "", err
	}
	types, err := s.Queries.AnnotationTypes(r.Context())
	if err != nil {
		return "", err
	}
	p.Title, p.Nav = d.Fermentation.Name, "fermentations"
	p.Data = fermentationData{EventTypes: types, D: d, Stats: statCards(d, p.Prefs), Log: buildLog(d, p.Prefs), Candidates: cands,
		LastAt: d.Device.LastSeenAt}
	return "fermentation", nil
}

// buildLog merges hand measurements and timeline events, oldest first.
func buildLog(d domain.FermentationDetail, p domain.Preferences) []logEntry {
	var out []logEntry
	for _, m := range d.Manual {
		v := formatGravity(p, m.GravitySG)
		if m.TemperatureC != nil {
			v += " · " + formatTemp(p, *m.TemperatureC)
		}
		kind := methodName(m.Method)
		switch {
		case m.IsOG:
			kind += " · OG"
		case m.IsFG:
			kind += " · FG"
		}
		out = append(out, logEntry{At: m.TakenAt, Kind: kind, Value: v, Note: m.Note,
			DeleteURL: "/manual/" + strconv.FormatInt(m.ID, 10) + "/delete", Confirm: "Remove this measurement?"})
	}
	for _, a := range d.Annotations {
		out = append(out, logEntry{At: a.At, Kind: a.Kind, Note: a.Note,
			DeleteURL: "/annotations/" + strconv.FormatInt(a.ID, 10) + "/delete", Confirm: "Remove this event?"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func methodName(m domain.MeasureMethod) string {
	switch m {
	case domain.MethodRefractometer:
		return "Refractometer"
	case domain.MethodHydrometer:
		return "Hydrometer"
	}
	return "Measurement"
}

// statCards builds the two tiers. Current gravity is a 6 h median, so when the
// latest reading differs noticeably it is shown beneath for comparison.
func statCards(d domain.FermentationDetail, p domain.Preferences) statTiers {
	st := d.Stats
	g := func(v *float64) string {
		if v == nil {
			return "–"
		}
		return formatGravity(p, *v)
	}
	num := func(v *float64, dec int, unit string) string {
		if v == nil {
			return "–"
		}
		return strconv.FormatFloat(*v, 'f', dec, 64) + unit
	}

	current := statCard{ID: "cur", Label: "Current gravity", Value: g(st.CurrentSG),
		Help: "The most recent reading."}
	if d.MeasuredFG != nil {
		current.Sub = "measured FG " + g(d.MeasuredFG)
	}

	trend, trendSub := trendText(p, st)
	ogSrc := "estimated"
	for _, m := range d.Manual {
		if m.IsOG {
			ogSrc = "measured"
		}
	}
	if ogSrc == "estimated" && d.Fermentation.OGOverride != nil {
		ogSrc = "override"
	}
	// Average is the headline; the range sits beneath it.
	tempAvg, tempRange := "–", ""
	if st.TempMinC != nil {
		tempAvg = formatTemp(p, *st.TempAvgC)
		tempRange = fmt.Sprintf("%s – %s", trimTemp(p, *st.TempMinC), formatTemp(p, *st.TempMaxC))
	}
	return statTiers{
		Hero: []statCard{
			current,
			{ID: "att", Label: "Attenuation", Value: num(st.Attenuation, 0, "%"),
				Help: "Apparent attenuation: how much of the sugar has fermented, from OG and current gravity."},
			{ID: "abv", Label: "ABV (estimate)", Value: num(st.ABV, 1, "%"),
				Help: "(OG − current gravity) × 131.25."},
			{ID: "trend", Label: "Trend", Value: trend, Sub: trendSub,
				Help: "Gravity change per day over the last 24 hours."},
		},
		Detail: []statCard{
			{ID: "og", Label: "Original gravity", Value: g(st.OG), Sub: ogSrc},
			{ID: "temp", Label: "Avg temperature", Value: tempAvg, Sub: tempRange},
			{ID: "n", Label: "Readings", Value: strconv.Itoa(st.Readings), Sub: formatDuration(st.Duration)},
		},
	}
}

func trimTemp(p domain.Preferences, c float64) string {
	return strconv.FormatFloat(p.ConvertTemp(c), 'f', 1, 64)
}

// trendText formats the daily gravity change: points per day for SG, °P/day
// for Plato, or "stable" when it is negligible.
func trendText(p domain.Preferences, st domain.Stats) (string, string) {
	if st.RatePerDay == nil {
		return "–", ""
	}
	cur := 1.0
	if st.CurrentSG != nil {
		cur = *st.CurrentSG
	}
	d := p.ConvertGravity(cur+*st.RatePerDay) - p.ConvertGravity(cur)
	if p.Gravity == domain.DisplayPlato {
		if math.Abs(d) < 0.05 {
			return "stable", ""
		}
		return fmt.Sprintf("%+.2f °P/day", d), ""
	}
	if math.Abs(d) < 0.0003 {
		return "stable", ""
	}
	return fmt.Sprintf("%+.1f pts/day", d*1000), ""
}

// deviceRow is one device with everything its card shows.
type deviceRow struct {
	domain.DeviceStatus
	Calibration *domain.Calibration
}

func (s *Server) devicesPage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	sts, err := s.Queries.Dashboard(r.Context())
	if err != nil {
		return "", err
	}
	cals, err := s.Queries.DeviceCalibrations(r.Context())
	if err != nil {
		return "", err
	}
	rows := make([]deviceRow, 0, len(sts))
	for _, st := range sts {
		row := deviceRow{DeviceStatus: st}
		if c, ok := cals[st.Device.ID]; ok {
			row.Calibration = &c
		}
		rows = append(rows, row)
	}
	p.Title, p.Nav, p.Data = "Devices", "devices", rows
	return "devices", nil
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, p *page) (string, error) {
	types, err := s.Queries.AnnotationTypes(r.Context())
	if err != nil {
		return "", err
	}
	d := settingsData{Types: types}
	if p.Admin && s.IngestToken != "" {
		d.Ingest = &ingestInfo{Token: s.IngestToken, Path: "/api/v1/ingest/ispindel", Port: listenPort(s.IngestListen)}
	}
	p.Title, p.Nav, p.Data = "Settings", "settings", d
	return "settings", nil
}

type settingsData struct {
	Types  []domain.AnnotationType
	Ingest *ingestInfo // nil unless the viewer is the admin
}

type ingestInfo struct{ Token, Path, Port string }

// listenPort returns the port of a listen address such as ":8080" or "0.0.0.0:8080".
func listenPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return ""
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: bad id", domain.ErrNotFound)
	}
	return id, nil
}

func formatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	}
	return fmt.Sprintf("%.1f days", d.Hours()/24)
}

func formatGravity(p domain.Preferences, sg float64) string {
	if sg <= 0 {
		return "–"
	}
	return strconv.FormatFloat(p.ConvertGravity(sg), 'f', p.GravityDecimals(), 64) + " " + p.GravityLabel()
}

func formatTemp(p domain.Preferences, c float64) string {
	return strconv.FormatFloat(p.ConvertTemp(c), 'f', 1, 64) + " " + p.TempLabel()
}

func pluralise(n int, unit string) string {
	if unit == "day" && n != 1 {
		unit = "days"
	}
	return strconv.Itoa(n) + " " + unit
}

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
