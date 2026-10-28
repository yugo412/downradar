package main

import (
	"io"
	"log/slog"
	"testing"

	"downradar.info/backend/internal/config"
)

func TestSummaryGeneratorHonoursTheSwitch(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		SummaryEnabled: false,
		OpenAIAPIKey:   "test-key",
		OpenAIModel:    "gpt-6-luna",
	}

	if generator := newSummaryGenerator(cfg, nil, log); generator != nil {
		t.Fatal("a disabled summary switch still created the generator")
	}

	cfg.SummaryEnabled = true
	if generator := newSummaryGenerator(cfg, nil, log); generator == nil {
		t.Fatal("an enabled summary switch did not create the generator")
	}

	cfg.OpenAIAPIKey = ""
	if generator := newSummaryGenerator(cfg, nil, log); generator != nil {
		t.Fatal("a missing API key still created the generator")
	}
}
