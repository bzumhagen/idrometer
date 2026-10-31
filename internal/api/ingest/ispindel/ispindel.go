// Package ispindel decodes the iSpindel "generic HTTP" JSON payload.
package ispindel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bzumhagen/idrometer/internal/domain"
)

// Decoder implements ingest.Decoder.
type Decoder struct{}

func (Decoder) Source() string { return "ispindel" }

// payload mirrors what the firmware sends. ID is a number on most firmware
// versions but is accepted as a string too.
type payload struct {
	Name        string          `json:"name"`
	ID          json.RawMessage `json:"ID"`
	Token       string          `json:"token"`
	Angle       float64         `json:"angle"`
	Temperature float64         `json:"temperature"`
	TempUnits   string          `json:"temp_units"`
	Battery     float64         `json:"battery"`
	Gravity     float64         `json:"gravity"`
	Interval    int             `json:"interval"`
	RSSI        int             `json:"RSSI"`
}

func (Decoder) Decode(body []byte) (domain.ReadingInput, string, error) {
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return domain.ReadingInput{}, "", fmt.Errorf("%w: malformed JSON: %w", domain.ErrInvalid, err)
	}
	id, err := hardwareID(p.ID)
	if err != nil {
		return domain.ReadingInput{}, "", err
	}
	var tempC float64
	switch strings.ToUpper(strings.TrimSpace(p.TempUnits)) {
	case "", "C":
		tempC = p.Temperature
	case "F":
		tempC = domain.FToC(p.Temperature)
	case "K":
		tempC = domain.KToC(p.Temperature)
	default:
		return domain.ReadingInput{}, "", fmt.Errorf("%w: unknown temp_units %q", domain.ErrInvalid, p.TempUnits)
	}
	// Keep the payload for later backfill, but never persist the token.
	raw, _ := json.Marshal(redact(body))
	return domain.ReadingInput{
		Source: "ispindel", HardwareID: id, Name: p.Name,
		Angle: p.Angle, TemperatureC: tempC, BatteryV: p.Battery, Gravity: p.Gravity,
		RSSI: p.RSSI, IntervalS: p.Interval, RawPayload: raw,
	}, p.Token, nil
}

func hardwareID(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("%w: missing ID", domain.ErrInvalid)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return "", fmt.Errorf("%w: empty ID", domain.ErrInvalid)
		}
		return s, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", fmt.Errorf("%w: ID must be a number or string", domain.ErrInvalid)
	}
	if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
		return "", fmt.Errorf("%w: ID must be an integer", domain.ErrInvalid)
	}
	return n.String(), nil
}

func redact(body []byte) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(body, &m)
	delete(m, "token")
	return m
}
