package config

import (
	"testing"
)

func TestLoadDBPath(t *testing.T) {
	t.Setenv("DB_PATH", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "vacation.db" {
		t.Fatalf("expected default DB_PATH, got %q", cfg.DBPath)
	}

	t.Setenv("DB_PATH", "/data/app.db")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "/data/app.db" {
		t.Fatalf("DB_PATH not honored: %q", cfg.DBPath)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CSRF_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected HTTPAddr: %q", cfg.HTTPAddr)
	}
	if len(cfg.CSRFKey) < 32 {
		t.Fatalf("ephemeral CSRF key too short: %d", len(cfg.CSRFKey))
	}
}

func TestLoadProductionRequiresCSRFKey(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("CSRF_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error: production without CSRF_KEY")
	}
}
