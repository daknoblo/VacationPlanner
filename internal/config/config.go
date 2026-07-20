// Package config loads and validates runtime configuration from the environment.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds all runtime settings for the service.
type Config struct {
	Env           string
	HTTPAddr      string
	DBPath        string
	OpenAIBaseURL string
	OpenAIAPIKey  string
	OpenAIModel   string
	CSRFKey       []byte

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration
	MaxRequestBytes   int64
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Env:           getenv("APP_ENV", "development"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		DBPath:        getenv("DB_PATH", "vacation.db"),
		OpenAIBaseURL: strings.TrimRight(getenv("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		OpenAIAPIKey:  os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:   getenv("OPENAI_MODEL", "gpt-4o-mini"),

		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second, // AI calls may stream slowly
		IdleTimeout:       120 * time.Second,
		RequestTimeout:    100 * time.Second,
		MaxRequestBytes:   1 << 20, // 1 MiB
	}

	if strings.TrimSpace(cfg.DBPath) == "" {
		return nil, fmt.Errorf("config: DB_PATH must not be empty")
	}

	key, err := loadCSRFKey(cfg.Env)
	if err != nil {
		return nil, err
	}
	cfg.CSRFKey = key

	return cfg, nil
}

// IsProduction reports whether the service runs in a production-like environment.
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production") || strings.EqualFold(c.Env, "prod")
}

func loadCSRFKey(env string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("CSRF_KEY"))
	if raw == "" {
		if strings.EqualFold(env, "production") || strings.EqualFold(env, "prod") {
			return nil, fmt.Errorf("config: CSRF_KEY is required in production (generate with: openssl rand -hex 32)")
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("config: generating ephemeral CSRF key: %w", err)
		}
		slog.Warn("CSRF_KEY not set; generated an ephemeral key (tokens reset on restart). Set CSRF_KEY for production.")
		return key, nil
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("config: CSRF_KEY must be hex-encoded: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("config: CSRF_KEY must be at least 32 bytes (64 hex chars)")
	}
	return key, nil
}

// NewLogger builds a structured slog logger appropriate for the environment.
func NewLogger(env string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if strings.EqualFold(env, "production") || strings.EqualFold(env, "prod") {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
