package config

import (
	"strings"
	"testing"
)

func clearAzureEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AZURE_RESOURCE_ID", "AZURE_IMAGE_RESOURCE_ID", "AZURE_TENANT_ID",
		"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_ENDPOINT",
		"AZURE_DEPLOYMENT", "AZURE_MODELS", "AZURE_API_VERSION",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadDBPath(t *testing.T) {
	clearAzureEnv(t)
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
	clearAzureEnv(t)
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
	clearAzureEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("CSRF_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error: production without CSRF_KEY")
	}
}

func TestAzureIdentityOnlyConfiguration(t *testing.T) {
	clearAzureEnv(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("CSRF_KEY", "")
	t.Setenv("VP_API_KEY", "legacy-test-key")
	t.Setenv("AZURE_DEPLOYMENT", " production-chat ")
	t.Setenv("AZURE_MODELS", "gpt-4o, ,gpt-4.1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Azure.Requested() {
		t.Fatal("removed key/model configuration must not enable AI")
	}
	t.Setenv("AZURE_CLIENT_SECRET", "private-test-secret")
	_, err = Load()
	if err == nil || strings.Contains(err.Error(), "private-test-secret") {
		t.Fatal("partial identity must fail safely")
	}
	t.Setenv("AZURE_RESOURCE_ID", "/subscriptions/11111111-1111-4111-8111-111111111111/resourceGroups/travel/providers/Microsoft.CognitiveServices/accounts/travel-ai")
	t.Setenv("AZURE_TENANT_ID", "22222222-2222-4222-8222-222222222222")
	t.Setenv("AZURE_CLIENT_ID", "33333333-3333-4333-8333-333333333333")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Azure.Requested() || cfg.Azure.ClientSecret != "private-test-secret" {
		t.Fatal("explicit identity was not loaded")
	}
	t.Setenv("AZURE_ENDPOINT", "http://untrusted.invalid/old-route")
	t.Setenv("AZURE_API_VERSION", "unsupported-legacy-version")
	if _, err := Load(); err != nil {
		t.Fatal("obsolete overrides must not control the Foundry connection")
	}
	t.Setenv("AZURE_RESOURCE_ID", "https://travel-ai.services.ai.azure.com/api/projects/travel")
	if _, err := Load(); err == nil {
		t.Fatal("project URL must not be accepted as account ID")
	}
}
