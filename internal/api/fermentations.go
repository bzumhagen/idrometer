package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// FermentationReader is what the read-only JSON API needs from the domain.
type FermentationReader interface {
	Fermentation(ctx context.Context, id int64) (domain.FermentationDetail, error)
	Fermentations(ctx context.Context, f domain.FermentationFilter) ([]domain.FermentationSummary, error)
	Preferences(ctx context.Context) (domain.Preferences, error)
}

// MountFermentations registers the read-only fermentation endpoints. They are
// public, like the web UI's GET pages.
func MountFermentations(q FermentationReader, log *slog.Logger) func(*http.ServeMux) {
	if log == nil {
		log = slog.Default()
	}
	h := &fermentationHandlers{q: q, log: log}
	return func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/v1/fermentations", h.list)
		mux.HandleFunc("GET /api/v1/fermentations/{id}/series", h.series)
		mux.HandleFunc("GET /api/v1/fermentations/{id}/export.csv", h.exportCSV)
		mux.HandleFunc("GET /api/v1/fermentations/{id}/export.json", h.exportJSON)
	}
}

type fermentationHandlers struct {
	q   FermentationReader
	log *slog.Logger
}

func (h *fermentationHandlers) id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (h *fermentationHandlers) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.log.Error("api error", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// GET /api/v1/fermentations/{id}/series[?excluded=1]: columns in display units.
func (h *fermentationHandlers) series(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	d, err := h.q.Fermentation(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	prefs, err := h.q.Preferences(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	series := domain.BuildSeries(d.Readings, prefs, r.URL.Query().Get("excluded") == "1")
	series.AttachMarkers(d.Manual, d.Annotations, prefs)
	writeJSON(w, series)
}

type fermentationJSON struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Device      string     `json:"device"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	LastReading *time.Time `json:"last_reading_at,omitempty"`
	LatestSG    *float64   `json:"latest_sg,omitempty"`
}

func (h *fermentationHandlers) list(w http.ResponseWriter, r *http.Request) {
	fl := domain.FermentationFilter{Status: domain.FermentationStatus(r.URL.Query().Get("status"))}
	if fl.Status != "" && fl.Status != domain.StatusActive && fl.Status != domain.StatusCompleted {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	fs, err := h.q.Fermentations(r.Context(), fl)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]fermentationJSON, 0, len(fs))
	for _, f := range fs {
		j := fermentationJSON{ID: f.ID, Name: f.Name, Device: f.DeviceName, Status: string(f.Status),
			StartedAt: f.StartedAt, EndedAt: f.EndedAt}
		if f.HasReadings {
			last := f.LastAt
			j.LastReading = &last
			if f.LatestSG > 0 {
				sg := f.LatestSG
				j.LatestSG = &sg
			}
		}
		out = append(out, j)
	}
	writeJSON(w, out)
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func filename(name string, id int64, ext string) string {
	base := strings.Trim(unsafeFilename.ReplaceAllString(name, "_"), "_.")
	if base == "" {
		base = "fermentation"
	}
	return fmt.Sprintf("%s-%d.%s", base, id, ext)
}

// Exports use SG and °C regardless of display preferences. gravity_sg is the
// effective (calibrated) gravity, matching the graph; gravity_device_sg is what
// the device itself reported.
func (h *fermentationHandlers) exportCSV(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	d, err := h.q.Fermentation(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename(d.Fermentation.Name, id, "csv")+`"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"timestamp", "angle", "temperature_c", "gravity_sg", "gravity_device_sg", "battery_v", "rssi", "excluded"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, x := range d.Readings {
		g, gd := "", ""
		if x.Gravity > 0 {
			g = f(x.Gravity)
		}
		if x.GravityDevice > 0 {
			gd = f(x.GravityDevice)
		}
		_ = cw.Write([]string{x.ReceivedAt.UTC().Format(time.RFC3339), f(x.Angle), f(x.TemperatureC), g, gd,
			f(x.BatteryV), strconv.Itoa(x.RSSI), strconv.FormatBool(x.Excluded)})
	}
	cw.Flush()
}

type readingJSON struct {
	Timestamp    time.Time `json:"timestamp"`
	Angle        float64   `json:"angle"`
	TemperatureC float64   `json:"temperature_c"`
	GravitySG    *float64  `json:"gravity_sg"`
	GravityDevSG *float64  `json:"gravity_device_sg"`
	BatteryV     float64   `json:"battery_v"`
	RSSI         int       `json:"rssi"`
	Excluded     bool      `json:"excluded"`
}

func (h *fermentationHandlers) exportJSON(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	d, err := h.q.Fermentation(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	rs := make([]readingJSON, 0, len(d.Readings))
	for _, x := range d.Readings {
		j := readingJSON{Timestamp: x.ReceivedAt.UTC(), Angle: x.Angle, TemperatureC: x.TemperatureC,
			BatteryV: x.BatteryV, RSSI: x.RSSI, Excluded: x.Excluded}
		if x.Gravity > 0 {
			g := x.Gravity
			j.GravitySG = &g
		}
		if x.GravityDevice > 0 {
			g := x.GravityDevice
			j.GravityDevSG = &g
		}
		rs = append(rs, j)
	}
	f := d.Fermentation
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename(f.Name, id, "json")+`"`)
	writeJSON(w, map[string]any{
		"id": f.ID, "name": f.Name, "device": d.Device.Name(), "status": f.Status,
		"started_at": f.StartedAt, "ended_at": f.EndedAt, "notes": f.Notes, "readings": rs,
	})
}
