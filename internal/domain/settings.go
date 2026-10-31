package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	SettingGapThreshold = "gap_threshold" // Go duration string, e.g. "72h"
	DefaultGapThreshold = 72 * time.Hour
)

// gapFor returns the gap after which a device's next reading starts a new
// fermentation: the device override, else the global setting, else 72h.
func gapFor(ctx context.Context, repos Repos, d Device) (time.Duration, error) {
	if d.GapThreshold != nil {
		return *d.GapThreshold, nil
	}
	v, err := repos.Settings.Get(ctx, SettingGapThreshold)
	if errors.Is(err, ErrNotFound) {
		return DefaultGapThreshold, nil
	}
	if err != nil {
		return 0, err
	}
	gap, err := time.ParseDuration(v)
	if err != nil || gap <= 0 {
		return 0, fmt.Errorf("%w: setting %s=%q", ErrInvalid, SettingGapThreshold, v)
	}
	return gap, nil
}
