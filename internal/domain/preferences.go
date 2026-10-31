package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type TempUnit string

const (
	TempC TempUnit = "c"
	TempF TempUnit = "f"
)

// DisplayGravity is how gravity is shown. Storage is always SG.
type DisplayGravity string

const (
	DisplaySG    DisplayGravity = "sg"
	DisplayPlato DisplayGravity = "plato"
)

const (
	settingDisplayGravity = "display_gravity"
	settingDisplayTemp    = "display_temp"
)

// Preferences are the global, user-editable settings.
type Preferences struct {
	Gravity DisplayGravity
	Temp    TempUnit
	Gap     time.Duration // global gap that starts a new fermentation
}

// DefaultPreferences are used for anything not yet stored.
func DefaultPreferences() Preferences {
	return Preferences{Gravity: DisplaySG, Temp: TempC, Gap: DefaultGapThreshold}
}

func (p Preferences) validate() error {
	if p.Gravity != DisplaySG && p.Gravity != DisplayPlato {
		return fmt.Errorf("%w: gravity unit %q", ErrInvalid, p.Gravity)
	}
	if p.Temp != TempC && p.Temp != TempF {
		return fmt.Errorf("%w: temperature unit %q", ErrInvalid, p.Temp)
	}
	if p.Gap < time.Hour || p.Gap > 365*24*time.Hour {
		return fmt.Errorf("%w: gap must be between 1 hour and 365 days", ErrInvalid)
	}
	return nil
}

// PreferenceService reads and writes Preferences.
type PreferenceService struct {
	tx  TxManager
	pub EventPublisher
}

func NewPreferenceService(tx TxManager, pub EventPublisher) *PreferenceService {
	return &PreferenceService{tx: tx, pub: pub}
}

func loadPreferences(ctx context.Context, r Repos) (Preferences, error) {
	p := DefaultPreferences()
	get := func(key string, set func(string)) error {
		v, err := r.Settings.Get(ctx, key)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err == nil {
			set(v)
		}
		return err
	}
	if err := get(settingDisplayGravity, func(v string) { p.Gravity = DisplayGravity(v) }); err != nil {
		return p, err
	}
	if err := get(settingDisplayTemp, func(v string) { p.Temp = TempUnit(v) }); err != nil {
		return p, err
	}
	if err := get(SettingGapThreshold, func(v string) {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			p.Gap = d
		}
	}); err != nil {
		return p, err
	}
	return p, nil
}

func (s *PreferenceService) Get(ctx context.Context) (Preferences, error) {
	var p Preferences
	err := s.tx.View(ctx, func(r Repos) (err error) { p, err = loadPreferences(ctx, r); return })
	return p, err
}

func (s *PreferenceService) Update(ctx context.Context, p Preferences) error {
	if err := p.validate(); err != nil {
		return err
	}
	err := s.tx.WithinTx(ctx, func(r Repos) error {
		for k, v := range map[string]string{
			settingDisplayGravity: string(p.Gravity),
			settingDisplayTemp:    string(p.Temp),
			SettingGapThreshold:   p.Gap.String(),
		} {
			if err := r.Settings.Set(ctx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.pub.Publish(Event{Type: EventSettings})
	}
	return err
}

// ConvertGravity converts stored SG to the display unit.
func (p Preferences) ConvertGravity(sg float64) float64 {
	if p.Gravity == DisplayPlato {
		return SGToPlato(sg)
	}
	return sg
}

// ConvertTemp converts stored °C to the display unit.
func (p Preferences) ConvertTemp(c float64) float64 {
	if p.Temp == TempF {
		return CToF(c)
	}
	return c
}

func (p Preferences) GravityLabel() string {
	if p.Gravity == DisplayPlato {
		return "°P"
	}
	return "SG"
}

func (p Preferences) TempLabel() string {
	if p.Temp == TempF {
		return "°F"
	}
	return "°C"
}

// GravityDecimals is how many decimals read well for the display unit.
func (p Preferences) GravityDecimals() int {
	if p.Gravity == DisplayPlato {
		return 1
	}
	return 3
}
