package domain

// Series is the column-oriented form of a fermentation's readings, ready for
// plotting. Values are in the user's display units. Gravity entries are nil
// where the device reported none.
type Series struct {
	T           []int64    `json:"t"` // unix seconds
	Gravity     []*float64 `json:"gravity"`
	Temp        []float64  `json:"temp"`
	Battery     []float64  `json:"battery"`
	RSSI        []int      `json:"rssi"`
	Excluded    []bool     `json:"excluded,omitempty"` // only when excluded readings are included
	GravityUnit string     `json:"gravityUnit"`
	TempUnit    string     `json:"tempUnit"`
	// Manual measurements and events, for overlaying on the graph.
	Manual      []ManualPoint     `json:"manual"`
	Annotations []AnnotationPoint `json:"annotations"`
}

type ManualPoint struct {
	T       int64   `json:"t"`
	Gravity float64 `json:"gravity"` // display unit
	Method  string  `json:"method"`
	Kind    string  `json:"kind,omitempty"` // "OG", "FG" or ""
}

type AnnotationPoint struct {
	T    int64  `json:"t"`
	Kind string `json:"kind"`
	Note string `json:"note"`
}

// AttachMarkers adds manual readings and annotations to a series.
func (s *Series) AttachMarkers(manual []ManualReading, ann []Annotation, p Preferences) {
	s.Manual, s.Annotations = []ManualPoint{}, []AnnotationPoint{}
	for _, m := range manual {
		pt := ManualPoint{T: m.TakenAt.Unix(), Gravity: p.ConvertGravity(m.GravitySG), Method: string(m.Method)}
		switch {
		case m.IsOG:
			pt.Kind = "OG"
		case m.IsFG:
			pt.Kind = "FG"
		}
		s.Manual = append(s.Manual, pt)
	}
	for _, a := range ann {
		label := a.Kind
		if a.Note != "" {
			label += ": " + a.Note
		}
		s.Annotations = append(s.Annotations, AnnotationPoint{T: a.At.Unix(), Kind: a.Kind, Note: label})
	}
}

// SeriesPoint converts a single reading, for live updates.
type SeriesPoint struct {
	T       int64    `json:"t"`
	Gravity *float64 `json:"gravity"`
	Temp    float64  `json:"temp"`
	Battery float64  `json:"battery"`
	RSSI    int      `json:"rssi"`
}

func PointOf(r Reading, p Preferences) SeriesPoint {
	pt := SeriesPoint{
		T: r.ReceivedAt.Unix(), Temp: p.ConvertTemp(r.TemperatureC),
		Battery: r.BatteryV, RSSI: r.RSSI,
	}
	if r.Gravity > 0 {
		g := p.ConvertGravity(r.Gravity)
		pt.Gravity = &g
	}
	return pt
}

// BuildSeries converts readings (sorted by time) to plot columns. Excluded
// readings are dropped unless includeExcluded is set, in which case they are
// flagged in Series.Excluded.
func BuildSeries(rs []Reading, p Preferences, includeExcluded bool) Series {
	s := Series{
		T: []int64{}, Gravity: []*float64{}, Temp: []float64{}, Battery: []float64{}, RSSI: []int{},
		GravityUnit: p.GravityLabel(), TempUnit: p.TempLabel(),
		Manual: []ManualPoint{}, Annotations: []AnnotationPoint{},
	}
	if includeExcluded {
		s.Excluded = []bool{}
	}
	for _, r := range rs {
		if r.Excluded && !includeExcluded {
			continue
		}
		pt := PointOf(r, p)
		s.T = append(s.T, pt.T)
		s.Gravity = append(s.Gravity, pt.Gravity)
		s.Temp = append(s.Temp, pt.Temp)
		s.Battery = append(s.Battery, pt.Battery)
		s.RSSI = append(s.RSSI, pt.RSSI)
		if includeExcluded {
			s.Excluded = append(s.Excluded, r.Excluded)
		}
	}
	return s
}
