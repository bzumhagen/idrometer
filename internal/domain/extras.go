package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// Recipe is a reusable template; fermentations may link to one.
type Recipe struct {
	ID        int64
	Name      string
	Style     string
	TargetOG  *float64 // SG
	TargetFG  *float64 // SG
	Yeast     string
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (r Recipe) validate() error {
	if r.Name == "" {
		return fmt.Errorf("%w: recipe name must not be empty", ErrInvalid)
	}
	for _, g := range []*float64{r.TargetOG, r.TargetFG} {
		if g != nil && (*g < 0.99 || *g > 1.2) {
			return fmt.Errorf("%w: target gravity must be an SG between 0.990 and 1.200", ErrInvalid)
		}
	}
	return nil
}

type MeasureMethod string

const (
	MethodHydrometer    MeasureMethod = "hydrometer"
	MethodRefractometer MeasureMethod = "refractometer"
	MethodOther         MeasureMethod = "other"
)

func (m MeasureMethod) Valid() bool {
	return m == MethodHydrometer || m == MethodRefractometer || m == MethodOther
}

// ManualReading is a gravity measurement entered by hand, such as the OG and
// FG checks taken with a hydrometer or refractometer.
type ManualReading struct {
	ID             int64
	FermentationID int64
	TakenAt        time.Time
	GravitySG      float64
	TemperatureC   *float64
	Method         MeasureMethod
	IsOG           bool
	IsFG           bool
	Note           string
}

func (m ManualReading) validate() error {
	switch {
	case m.GravitySG < 0.98 || m.GravitySG > 1.2 || math.IsNaN(m.GravitySG):
		return fmt.Errorf("%w: gravity must be between 0.980 and 1.200 SG", ErrInvalid)
	case !m.Method.Valid():
		return fmt.Errorf("%w: unknown method %q", ErrInvalid, m.Method)
	case m.IsOG && m.IsFG:
		return fmt.Errorf("%w: a reading cannot be both OG and FG", ErrInvalid)
	case m.TakenAt.IsZero():
		return fmt.Errorf("%w: time is required", ErrInvalid)
	}
	return nil
}

// AnnotationType is a user-defined kind of timeline event ("Dry hop", "Added fruit"...).
type AnnotationType struct {
	ID   int64
	Name string
}

// DefaultAnnotationTypes are the types a new installation starts with.
var DefaultAnnotationTypes = []string{"Pitched yeast", "Dry hop", "Temperature change", "Cold crash", "Transfer", "Note"}

const maxAnnotationTypeName = 40

// cleanAnnotationTypeName trims a name and checks its length.
func cleanAnnotationTypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: event type name must not be empty", ErrInvalid)
	case utf8.RuneCountInString(name) > maxAnnotationTypeName:
		return "", fmt.Errorf("%w: event type name is longer than %d characters", ErrInvalid, maxAnnotationTypeName)
	}
	return name, nil
}

// Annotation marks an event on a fermentation's timeline.
type Annotation struct {
	ID             int64
	FermentationID int64
	At             time.Time
	Kind           string // the event type's name when logged
	Note           string
}

func (a Annotation) validate() error {
	switch {
	case strings.TrimSpace(a.Kind) == "":
		return fmt.Errorf("%w: event type is required", ErrInvalid)
	case a.At.IsZero():
		return fmt.Errorf("%w: time is required", ErrInvalid)
	}
	return nil
}

// Calibration converts a device's tilt angle to gravity with a cubic
// polynomial: gravity = C[0] + C[1]·angle + C[2]·angle² + C[3]·angle³.
// When a device has one, it replaces the gravity the device reported, for all
// of its readings, past and future.
type Calibration struct {
	DeviceID   int64
	C          [4]float64
	OutputUnit GravityUnit // sg or plato: what the polynomial yields
	Note       string
	UpdatedAt  time.Time
}

func (c Calibration) validate() error {
	for _, v := range c.C {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: coefficients must be finite numbers", ErrInvalid)
		}
	}
	if c.C == [4]float64{} {
		return fmt.Errorf("%w: all coefficients are zero", ErrInvalid)
	}
	if c.OutputUnit != GravitySG && c.OutputUnit != GravityPlato {
		return fmt.Errorf("%w: calibration unit must be sg or plato", ErrInvalid)
	}
	return nil
}

// Gravity returns the calibrated gravity in SG for a tilt angle.
func (c Calibration) Gravity(angle float64) float64 {
	v := c.C[0] + angle*(c.C[1]+angle*(c.C[2]+angle*c.C[3]))
	if c.OutputUnit == GravityPlato {
		return PlatoToSG(v)
	}
	return v
}

// effectiveGravity is the gravity to show for a reading: calibrated when the
// device has a calibration, otherwise what the device reported (0 = none).
func effectiveGravity(r Reading, cal *Calibration) float64 {
	if cal != nil {
		if g := cal.Gravity(r.Angle); g > 0 && !math.IsNaN(g) && !math.IsInf(g, 0) {
			return g
		}
		return 0
	}
	return r.GravityDevice
}
