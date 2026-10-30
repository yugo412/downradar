package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"downradar.info/backend/internal/config"
)

func TestSummaryGeneratorNeedsItsConfiguration(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		SummaryEnabled: true,
		OpenAIAPIKey:   "test-key",
		OpenAIModel:    "test-model",
		OpenAIBaseURL:  "https://example.com/v1",
		OpenAITimeout:  time.Minute,
	}

	if generator := newSummaryGenerator(cfg, nil, log); generator == nil {
		t.Fatal("a complete summary configuration did not create the generator")
	}

	cfg.SummaryEnabled = false
	if generator := newSummaryGenerator(cfg, nil, log); generator != nil {
		t.Fatal("a disabled summary switch still created the generator")
	}
	cfg.SummaryEnabled = true

	for name, clear := range map[string]func(*config.Config){
		"key":     func(c *config.Config) { c.OpenAIAPIKey = "" },
		"model":   func(c *config.Config) { c.OpenAIModel = "" },
		"baseURL": func(c *config.Config) { c.OpenAIBaseURL = "" },
		"timeout": func(c *config.Config) { c.OpenAITimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			clear(&c)
			if generator := newSummaryGenerator(c, nil, log); generator != nil {
				t.Fatal("an incomplete summary configuration created the generator")
			}
		})
	}
}
