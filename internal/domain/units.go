package domain

import (
	"fmt"
	"math"
)

// GravityUnit is the unit a device reports gravity in.
type GravityUnit string

const (
	GravityAuto  GravityUnit = "auto" // values < 2 are SG, otherwise Plato
	GravitySG    GravityUnit = "sg"
	GravityPlato GravityUnit = "plato"
)

// Valid reports whether u is a known unit.
func (u GravityUnit) Valid() bool {
	return u == GravityAuto || u == GravitySG || u == GravityPlato
}

// PlatoToSG converts degrees Plato to specific gravity.
func PlatoToSG(p float64) float64 {
	return 1 + p/(258.6-(p/258.2)*227.1)
}

// SGToPlato converts specific gravity to degrees Plato.
func SGToPlato(sg float64) float64 {
	return -616.868 + 1111.14*sg - 630.272*sg*sg + 135.997*sg*sg*sg
}

func CToF(c float64) float64 { return c*9/5 + 32 }
func FToC(f float64) float64 { return (f - 32) * 5 / 9 }
func KToC(k float64) float64 { return k - 273.15 }

// NormalizeGravity converts a device-reported gravity value to SG.
// A value of 0 means "no gravity reported" and is returned unchanged.
func NormalizeGravity(v float64, unit GravityUnit) (float64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, fmt.Errorf("%w: gravity %v", ErrInvalid, v)
	}
	if v == 0 {
		return 0, nil
	}
	switch unit {
	case GravitySG:
		return v, nil
	case GravityPlato:
		return PlatoToSG(v), nil
	case GravityAuto, "":
		if v < 2 {
			return v, nil
		}
		return PlatoToSG(v), nil
	}
	return 0, fmt.Errorf("%w: gravity unit %q", ErrInvalid, unit)
}
