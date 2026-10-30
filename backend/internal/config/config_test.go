package config

import (
	"testing"
	"time"
)

func TestSummaryProviderSettingsComeFromEnv(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "test-model")
	t.Setenv("OPENAI_BASE_URL", "https://example.com/v1")
	t.Setenv("OPENAI_TIMEOUT", "45s")

	cfg := Load()
	if cfg.OpenAIModel != "test-model" {
		t.Errorf("OpenAIModel = %q", cfg.OpenAIModel)
	}
	if cfg.OpenAIBaseURL != "https://example.com/v1" {
		t.Errorf("OpenAIBaseURL = %q", cfg.OpenAIBaseURL)
	}
	if cfg.OpenAITimeout != 45*time.Second {
		t.Errorf("OpenAITimeout = %v", cfg.OpenAITimeout)
	}
}

func TestSummaryProviderSettingsHaveNoDefaults(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_TIMEOUT", "")

	cfg := Load()
	if cfg.OpenAIModel != "" || cfg.OpenAIBaseURL != "" || cfg.OpenAITimeout != 0 {
		t.Errorf("provider settings have defaults: model=%q baseURL=%q timeout=%v",
			cfg.OpenAIModel, cfg.OpenAIBaseURL, cfg.OpenAITimeout)
	}
}

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
