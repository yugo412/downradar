package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL           string
	HTTPAddr              string
	ServicesFile          string
	PollConcurrency       int
	PollTick              time.Duration
	RequestTimeout        time.Duration
	RateLimitPerMinute    int
	RateLimitBurst        int
	SuggestPerHour        int
	SuggestBurst          int
	InternalToken         string
	RawPayloadDays        int
	RawPayloadMinInterval time.Duration
	LogLevel              string
}

func Load() Config {
	return Config{
		DatabaseURL:           env("DATABASE_URL", "postgres://downradar:downradar@localhost:5433/downradar?sslmode=disable"),
		HTTPAddr:              env("HTTP_ADDR", ":8080"),
		ServicesFile:          env("SERVICES_FILE", "services.yaml"),
		PollConcurrency:       envInt("POLL_CONCURRENCY", 8),
		PollTick:              envDuration("POLL_TICK", 5*time.Second),
		RequestTimeout:        envDuration("REQUEST_TIMEOUT", 20*time.Second),
		RateLimitPerMinute:    envInt("RATE_LIMIT_PER_MINUTE", 120),
		RateLimitBurst:        envInt("RATE_LIMIT_BURST", 30),
		SuggestPerHour:        envInt("SUGGEST_RATE_LIMIT_PER_HOUR", 6),
		SuggestBurst:          envInt("SUGGEST_RATE_LIMIT_BURST", 5),
		InternalToken:         env("INTERNAL_TOKEN", ""),
		RawPayloadDays:        envInt("RAW_PAYLOAD_RETENTION_DAYS", 7),
		RawPayloadMinInterval: envDuration("RAW_PAYLOAD_MIN_INTERVAL", time.Hour),
		LogLevel:              env("LOG_LEVEL", "info"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
