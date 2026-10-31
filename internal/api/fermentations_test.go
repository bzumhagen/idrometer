package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bzumhagen/idrometer/internal/api"
	"github.com/bzumhagen/idrometer/internal/domain"
	"github.com/bzumhagen/idrometer/internal/store/memory"
)

type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

// setup ingests n readings 15 minutes apart and returns a router.
func setup(t *testing.T, n int) (http.Handler, int64) {
	t.Helper()
	store := memory.New()
	clock := &fixedClock{time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	ing := domain.NewIngestor(store, clock, domain.NopPublisher{})
	var fid int64
	for i := 0; i < n; i++ {
		r, err := ing.Ingest(context.Background(), domain.ReadingInput{
			Source: "ispindel", HardwareID: "1", Name: "My Spindle", TemperatureC: 20 + float64(i),
			Gravity: 1.050 - float64(i)*0.001, Angle: 30, BatteryV: 4, RSSI: -60,
		})
		if err != nil {
			t.Fatal(err)
		}
		fid = r.FermentationID
		clock.t = clock.t.Add(15 * time.Minute)
	}
	q := domain.NewQueries(store, clock)
	return api.NewRouter(api.Deps{
		Ingest: http.NotFoundHandler(),
		Mount:  []func(*http.ServeMux){api.MountFermentations(q, nil)},
	}), fid
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSeries(t *testing.T) {
	h, id := setup(t, 4)
	rec := get(h, "/api/v1/fermentations/"+itoa(id)+"/series")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var s domain.Series
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.T) != 4 || *s.Gravity[0] != 1.05 || s.Temp[3] != 23 || s.GravityUnit != "SG" || s.TempUnit != "°C" {
		t.Fatalf("%+v", s)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("series must not be cached")
	}
}

func TestSeriesErrors(t *testing.T) {
	h, _ := setup(t, 1)
	for path, want := range map[string]int{
		"/api/v1/fermentations/999/series": 404,
		"/api/v1/fermentations/abc/series": 400,
		"/api/v1/fermentations/0/series":   400,
	} {
		if rec := get(h, path); rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

func TestExportCSV(t *testing.T) {
	h, id := setup(t, 3)
	rec := get(h, "/api/v1/fermentations/"+itoa(id)+"/export.csv")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatal(rec.Code, rec.Header())
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 4 || lines[0] != "timestamp,angle,temperature_c,gravity_sg,gravity_device_sg,battery_v,rssi,excluded" {
		t.Fatalf("csv:\n%s", rec.Body)
	}
	if !strings.HasPrefix(lines[1], "2026-10-01T12:00:00Z,30,20,1.05,1.05,4,-60,false") {
		t.Fatalf("row: %s", lines[1])
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `attachment; filename="My_Spindle_`) || strings.ContainsAny(cd, "·/\\") {
		t.Fatalf("disposition: %s", cd)
	}
}

func TestExportJSONAndList(t *testing.T) {
	h, id := setup(t, 3)
	var doc struct {
		Name     string
		Readings []struct {
			GravitySG *float64 `json:"gravity_sg"`
		}
	}
	rec := get(h, "/api/v1/fermentations/"+itoa(id)+"/export.json")
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Readings) != 3 || *doc.Readings[2].GravitySG != 1.048 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	var list []map[string]any
	rec = get(h, "/api/v1/fermentations?status=active")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0]["status"] != "active" {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if rec := get(h, "/api/v1/fermentations?status=bogus"); rec.Code != 400 {
		t.Fatalf("bad status: %d", rec.Code)
	}
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }

func TestExportUsesCalibratedGravity(t *testing.T) {
	store := memory.New()
	clock := &fixedClock{time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	ing := domain.NewIngestor(store, clock, domain.NopPublisher{})
	r, err := ing.Ingest(context.Background(), domain.ReadingInput{
		Source: "ispindel", HardwareID: "1", Name: "Cal", TemperatureC: 20, Gravity: 1.050, Angle: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.NewEditor(store, clock, domain.NopPublisher{}).SetCalibration(context.Background(),
		domain.Calibration{DeviceID: r.DeviceID, C: [4]float64{1, 0.001}, OutputUnit: domain.GravitySG}); err != nil {
		t.Fatal(err)
	}
	h := api.NewRouter(api.Deps{
		Ingest: http.NotFoundHandler(),
		Mount:  []func(*http.ServeMux){api.MountFermentations(domain.NewQueries(store, clock), nil)},
	})
	base := "/api/v1/fermentations/" + itoa(r.FermentationID)
	row := strings.Split(strings.TrimSpace(get(h, base+"/export.csv").Body.String()), "\n")[1]
	if !strings.Contains(row, ",20,1.03,1.05,") {
		t.Fatalf("csv row: %s", row)
	}
	var doc struct {
		Readings []struct {
			GravitySG       float64 `json:"gravity_sg"`
			GravityDeviceSG float64 `json:"gravity_device_sg"`
		}
	}
	if err := json.Unmarshal(get(h, base+"/export.json").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if g := doc.Readings[0]; g.GravitySG < 1.0299 || g.GravitySG > 1.0301 || g.GravityDeviceSG != 1.05 {
		t.Fatalf("%+v", g)
	}
}
