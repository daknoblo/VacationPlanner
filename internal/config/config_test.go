package config

import (
	"testing"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("APP_ENV", "development")
	t.Setenv("CSRF_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected HTTPAddr: %q", cfg.HTTPAddr)
	}
	if cfg.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Fatalf("unexpected base url: %q", cfg.OpenAIBaseURL)
	}
	if len(cfg.CSRFKey) < 32 {
		t.Fatalf("ephemeral CSRF key too short: %d", len(cfg.CSRFKey))
	}
}

func TestLoadProductionRequiresCSRFKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("CSRF_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error: production without CSRF_KEY")
	}
}

func TestLoadTrimsBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("OPENAI_BASE_URL", "https://example.com/v1/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAIBaseURL != "https://example.com/v1" {
		t.Fatalf("trailing slash not trimmed: %q", cfg.OpenAIBaseURL)
	}
}
