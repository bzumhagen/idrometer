package ispindel_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bzumhagen/idrometer/internal/api/ingest/ispindel"
	"github.com/bzumhagen/idrometer/internal/domain"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantID   string
		wantTemp float64
		wantErr  bool
	}{
		{"numeric id, celsius", `{"name":"iSpindel000","ID":1234567,"token":"tok","angle":22.2,"temperature":20.5,"temp_units":"C","battery":4.01,"gravity":1.0452,"interval":900,"RSSI":-70}`, "1234567", 20.5, false},
		{"string id", `{"name":"x","ID":"abc123","temperature":19,"angle":1,"battery":4,"gravity":1,"interval":900,"RSSI":-1}`, "abc123", 19, false},
		{"fahrenheit", `{"ID":1,"temperature":68,"temp_units":"F"}`, "1", 20, false},
		{"kelvin", `{"ID":1,"temperature":293.15,"temp_units":"K"}`, "1", 20, false},
		{"units omitted", `{"ID":1,"temperature":21}`, "1", 21, false},
		{"missing id", `{"temperature":21}`, "", 0, true},
		{"null id", `{"ID":null}`, "", 0, true},
		{"fractional id", `{"ID":1.5}`, "", 0, true},
		{"bad units", `{"ID":1,"temp_units":"R"}`, "", 0, true},
		{"not json", `nope`, "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, _, err := ispindel.Decoder{}.Decode([]byte(c.body))
			if c.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if in.Source != "ispindel" || in.HardwareID != c.wantID || math.Abs(in.TemperatureC-c.wantTemp) > 1e-9 {
				t.Fatalf("in = %+v", in)
			}
		})
	}
}

func TestDecodeReturnsTokenButDoesNotStoreIt(t *testing.T) {
	in, tok, err := ispindel.Decoder{}.Decode([]byte(`{"ID":1,"token":"s3cret-token","angle":5}`))
	if err != nil || tok != "s3cret-token" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if strings.Contains(string(in.RawPayload), "s3cret") || !strings.Contains(string(in.RawPayload), `"angle":5`) {
		t.Fatalf("raw payload = %s", in.RawPayload)
	}
}
