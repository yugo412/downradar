package adapters

import "testing"

func TestWorst(t *testing.T) {
	cases := []struct {
		name string
		in   []Health
		want Health
	}{
		{"no input", nil, HealthUnknown},
		{"single ok", []Health{HealthOK}, HealthOK},
		{"problem wins over ok", []Health{HealthOK, HealthDegraded, HealthPartial}, HealthPartial},
		{"major wins", []Health{HealthOK, HealthMajor, HealthDegraded}, HealthMajor},
		{"maintenance wins over ok", []Health{HealthOK, HealthMaintenance}, HealthMaintenance},
		{"unknown wins over ok", []Health{HealthOK, HealthUnknown}, HealthUnknown},
		{"unknown wins over maintenance", []Health{HealthMaintenance, HealthUnknown}, HealthUnknown},
		{"degraded wins over unknown", []Health{HealthUnknown, HealthDegraded}, HealthDegraded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Worst(tc.in...); got != tc.want {
				t.Errorf("Worst(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHealthHealthy(t *testing.T) {
	if !HealthOK.Healthy() || !HealthMaintenance.Healthy() {
		t.Error("ok and maintenance should be healthy")
	}
	for _, h := range []Health{HealthDegraded, HealthPartial, HealthMajor, HealthUnknown} {
		if h.Healthy() {
			t.Errorf("%q must not be healthy", h)
		}
	}
}
