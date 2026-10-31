package domain_test

import (
	"math"
	"os/exec"
	"strings"
	"testing"

	"github.com/bzumhagen/idrometer/internal/domain"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestPlatoSGRoundTrip(t *testing.T) {
	for _, sg := range []float64{1.000, 1.020, 1.050, 1.080, 1.110} {
		if got := domain.PlatoToSG(domain.SGToPlato(sg)); !near(got, sg, 0.0005) {
			t.Errorf("round trip %v -> %v", sg, got)
		}
	}
	if p := domain.SGToPlato(1.048); !near(p, 12.0, 0.1) {
		t.Errorf("SGToPlato(1.048) = %v, want ~12", p)
	}
}

func TestTemperatureConversions(t *testing.T) {
	if !near(domain.FToC(68), 20, 1e-9) || !near(domain.CToF(20), 68, 1e-9) || !near(domain.KToC(293.15), 20, 1e-9) {
		t.Fatal("temperature conversion wrong")
	}
}

func TestNormalizeGravity(t *testing.T) {
	cases := []struct {
		v    float64
		u    domain.GravityUnit
		want float64
	}{
		{0, domain.GravityAuto, 0},
		{1.050, domain.GravityAuto, 1.050},
		{1.050, domain.GravitySG, 1.050},
		{12, domain.GravityPlato, domain.PlatoToSG(12)},
		{12, domain.GravityAuto, domain.PlatoToSG(12)},
	}
	for _, c := range cases {
		got, err := domain.NormalizeGravity(c.v, c.u)
		if err != nil || !near(got, c.want, 1e-9) {
			t.Errorf("NormalizeGravity(%v,%q) = %v, %v; want %v", c.v, c.u, got, err, c.want)
		}
	}
	if _, err := domain.NormalizeGravity(-1, domain.GravityAuto); err == nil {
		t.Error("negative gravity should fail")
	}
}

// The domain must only depend on the standard library.
func TestDomainImportsOnlyStdlib(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if p != "github.com/bzumhagen/idrometer/internal/domain" {
			t.Errorf("domain depends on non-stdlib package %s", p)
		}
	}
}
