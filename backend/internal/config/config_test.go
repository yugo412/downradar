package config

import "testing"

func TestSummaryEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default", value: "", want: true},
		{name: "on", value: "true", want: true},
		{name: "off", value: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SUMMARY_ENABLED", tt.value)
			if got := Load().SummaryEnabled; got != tt.want {
				t.Errorf("SummaryEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}
