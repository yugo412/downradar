package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL             string
	HTTPAddr                string
	ServicesFile            string
	PollConcurrency         int
	PollTick                time.Duration
	RequestTimeout          time.Duration
	RateLimitPerMinute      int
	RateLimitBurst          int
	SuggestPerHour          int
	SuggestBurst            int
	LoginPerHour            int
	LoginBurst              int
	LinkPerHour             int
	LinkBurst               int
	PasswordPerHour         int
	PasswordBurst           int
	SessionCookie           string
	SessionTTL              time.Duration
	SessionCookieSecure     bool
	InternalToken           string
	RawPayloadDays          int
	RawPayloadMinInterval   time.Duration
	SMTPAddr                string
	SMTPUser                string
	SMTPPassword            string
	MailFrom                string
	TelegramAPIBase         string
	PublicURL               string
	GitHubClientID          string
	GitHubClientSecret      string
	ContactEmail            string
	AlertPerHour            int
	AlertBurst              int
	AlertLimitPerSubscriber int
	AccountPerHour          int
	AccountBurst            int
	ContactPerHour          int
	ContactBurst            int
	AlertSendInterval       time.Duration
	LogLevel                string
}

func Load() Config {
	return Config{
		DatabaseURL:             env("DATABASE_URL", "postgres://downradar:downradar@localhost:5433/downradar?sslmode=disable"),
		HTTPAddr:                env("HTTP_ADDR", ":8080"),
		ServicesFile:            env("SERVICES_FILE", "services.yaml"),
		PollConcurrency:         envInt("POLL_CONCURRENCY", 8),
		PollTick:                envDuration("POLL_TICK", 5*time.Second),
		RequestTimeout:          envDuration("REQUEST_TIMEOUT", 20*time.Second),
		RateLimitPerMinute:      envInt("RATE_LIMIT_PER_MINUTE", 120),
		RateLimitBurst:          envInt("RATE_LIMIT_BURST", 30),
		SuggestPerHour:          envInt("SUGGEST_RATE_LIMIT_PER_HOUR", 6),
		SuggestBurst:            envInt("SUGGEST_RATE_LIMIT_BURST", 5),
		LoginPerHour:            envInt("LOGIN_RATE_LIMIT_PER_HOUR", 20),
		LoginBurst:              envInt("LOGIN_RATE_LIMIT_BURST", 5),
		LinkPerHour:             envInt("LINK_RATE_LIMIT_PER_HOUR", 6),
		LinkBurst:               envInt("LINK_RATE_LIMIT_BURST", 5),
		PasswordPerHour:         envInt("PASSWORD_RATE_LIMIT_PER_HOUR", 20),
		PasswordBurst:           envInt("PASSWORD_RATE_LIMIT_BURST", 5),
		SessionCookie:           env("SESSION_COOKIE", "dr_session"),
		SessionTTL:              envDuration("SESSION_TTL", 30*24*time.Hour),
		SessionCookieSecure:     envBool("SESSION_COOKIE_SECURE", true),
		InternalToken:           env("INTERNAL_TOKEN", ""),
		RawPayloadDays:          envInt("RAW_PAYLOAD_RETENTION_DAYS", 7),
		RawPayloadMinInterval:   envDuration("RAW_PAYLOAD_MIN_INTERVAL", time.Hour),
		SMTPAddr:                env("SMTP_ADDR", ""),
		SMTPUser:                env("SMTP_USER", ""),
		SMTPPassword:            env("SMTP_PASSWORD", ""),
		MailFrom:                env("MAIL_FROM", "DownRadar <alerts@downradar.info>"),
		TelegramAPIBase:         env("TELEGRAM_API_BASE", "https://api.telegram.org"),
		PublicURL:               env("PUBLIC_URL", "http://localhost:3000"),
		GitHubClientID:          env("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:      env("GITHUB_CLIENT_SECRET", ""),
		ContactEmail:            env("CONTACT_EMAIL", "support@downradar.info"),
		AlertPerHour:            envInt("ALERT_RATE_LIMIT_PER_HOUR", 6),
		AlertBurst:              envInt("ALERT_RATE_LIMIT_BURST", 5),
		AlertLimitPerSubscriber: envInt("ALERT_LIMIT_PER_SUBSCRIBER", 10),
		AccountPerHour:          envInt("ACCOUNT_RATE_LIMIT_PER_HOUR", 5),
		AccountBurst:            envInt("ACCOUNT_RATE_LIMIT_BURST", 10),
		ContactPerHour:          envInt("CONTACT_RATE_LIMIT_PER_HOUR", 6),
		ContactBurst:            envInt("CONTACT_RATE_LIMIT_BURST", 5),
		AlertSendInterval:       envDuration("ALERT_SEND_INTERVAL", 30*time.Second),
		LogLevel:                env("LOG_LEVEL", "info"),
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

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
