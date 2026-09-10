package foundry

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfigValidationAndExplicitIdentity(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.ResourceID = "" },
		func(c *Config) { c.ResourceID += "/deployments/chat" },
		func(c *Config) { c.ResourceID = "https://main.services.ai.azure.com/api/projects/project" },
		func(c *Config) { c.TenantID = "" },
		func(c *Config) { c.ClientID = "" },
		func(c *Config) { c.ClientSecret = "" },
		func(c *Config) { c.ClientSecret = "  " },
		func(c *Config) { c.TenantID = "tenant.example" },
		func(c *Config) { c.ImageResourceID = "private-invalid-resource" },
	} {
		cfg := testConfig()
		change(&cfg)
		if err := cfg.Validate(); err == nil || strings.Contains(err.Error(), cfg.ClientSecret) && cfg.ClientSecret != "" {
			t.Fatalf("invalid or sensitive config error: %v", err)
		}
	}
	cfg := testConfig()
	cfg.ResourceID = strings.Replace(mainResource, "resourcegroups", "resourceGroups", 1) + "/"
	cfg.ImageResourceID = strings.ToUpper(mainResource)
	c, _, _ := testClient(t, cfg)
	if len(c.resources()) != 1 || c.cfg.ResourceID != mainResource || c.Config().ClientSecret != "" {
		t.Fatal("resource normalization or secret redaction failed")
	}
	if _, err := New(testConfig(), nil); err == nil {
		t.Fatal("nil persistent cache accepted")
	}
	if (Config{}).Requested() || (Config{}).Validate() != nil {
		t.Fatal("empty identity configuration must leave AI disabled")
	}
	for _, cfg := range []Config{{ResourceID: "x"}, {ImageResourceID: "x"}, {TenantID: "x"}, {ClientID: "x"}, {ClientSecret: "x"}} {
		if !cfg.Requested() {
			t.Fatal("partial identity was ignored")
		}
	}
}

func TestEndpointNormalizationAndBinding(t *testing.T) {
	for _, raw := range []string{
		"https://main.openai.azure.com", "https://main.openai.azure.com/",
		"https://main.openai.azure.com/openai/v1", "https://MAIN.openai.azure.com:443/openai/v1/",
	} {
		value, err := NormalizeEndpoint(raw)
		if err != nil || value != mainEndpoint {
			t.Fatalf("normalize %q = %q %v", raw, value, err)
		}
	}
	for _, raw := range []string{
		"http://main.openai.azure.com", "https://user:secret@main.openai.azure.com",
		"https://main.openai.azure.com/?q=secret", "https://main.openai.azure.com/#fragment",
		"https://main.openai.azure.com:444", "https://main.openai.azure.com//openai/v1",
		"https://main.openai.azure.com/openai/../openai/v1", "https://main.openai.azure.com/openai%2fv1",
		"https://main.openai.azure.com/api/projects/x", "https://main.openai.azure.com/openai/deployments/x",
		"https://main.cognitiveservices.azure.com", "https://main.openai.azure.com.attacker.example",
	} {
		if _, err := NormalizeEndpoint(raw); err == nil {
			t.Fatalf("unsafe endpoint accepted: %q", raw)
		}
	}
	properties := accountProperties{Endpoint: "https://main.cognitiveservices.azure.com", CustomSubDomainName: "main"}
	if _, err := selectEndpoint(properties); err == nil {
		t.Fatal("cognitive root guessed as OpenAI route")
	}
	properties.Endpoints = map[string]string{"openai": mainEndpoint}
	if value, err := selectEndpoint(properties); err != nil || value != mainEndpoint {
		t.Fatal("additional explicit metadata endpoint not used")
	}
	properties.Endpoints["other"] = "https://main.services.ai.azure.com/openai/v1"
	if value, err := selectEndpoint(properties); err != nil || value != mainEndpoint {
		t.Fatal("same-account endpoint aliases should prefer the advertised OpenAI endpoint")
	}
}

func TestCanonicalModelClassification(t *testing.T) {
	for _, tt := range []struct {
		model, format, state, sku string
		capabilities              map[string]string
		supported, temperature    bool
	}{
		{"gpt-4o", "OpenAI", "Succeeded", "Standard", nil, true, true},
		{"gpt-4.1-mini", "OpenAI", "Succeeded", "Standard", nil, true, true},
		{"gpt-5", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-5.5", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-5.6-luna", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-6-astra", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-chat-latest", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-5.99", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"gpt-5.4-pro", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"o1-preview", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"o3-mini", "OpenAI", "Succeeded", "Standard", nil, true, false},
		{"gpt-image-1", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"text-embedding-3-small", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"gpt-4o-realtime-preview", "OpenAI", "Succeeded", "Standard", nil, false, false},
		{"gpt-4o", "Anthropic", "Succeeded", "Standard", nil, false, false},
		{"unknown-gpt", "OpenAI", "Succeeded", "Standard", map[string]string{"chatCompletion": "true"}, false, false},
		{"gpt-4o", "OpenAI", "Creating", "Standard", nil, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "GlobalBatch", nil, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "Standard", map[string]string{"chatCompletion": "false"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "Standard", map[string]string{"chatCompletion": "unknown"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "Standard", map[string]string{"batchOnly": "true"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "Standard", map[string]string{"protocol": "anthropic"}, false, false},
	} {
		t.Run(tt.model+"-"+tt.format+"-"+tt.state+"-"+tt.sku, func(t *testing.T) {
			d := Deployment{Name: "arbitrary-alias", Model: tt.model, ModelFormat: tt.format,
				ProvisioningState: tt.state, SKU: map[string]any{"name": tt.sku}, Capabilities: tt.capabilities}
			classify(&d)
			if d.ChatSupported != tt.supported || d.SupportsTemperature != tt.temperature {
				t.Fatalf("wrong classification: %+v", d)
			}
			if !d.ChatSupported && d.Reason == "" {
				t.Fatal("unsupported model has no diagnosis")
			}
		})
	}
}

func TestConfigurationHasOnlyExplicitIdentityFields(t *testing.T) {
	want := []string{"ResourceID", "ImageResourceID", "TenantID", "ClientID", "ClientSecret"}
	cfgType := reflect.TypeFor[Config]()
	if cfgType.NumField() != len(want) {
		t.Fatal("configuration contains non-identity settings")
	}
	for _, name := range want {
		if _, ok := cfgType.FieldByName(name); !ok {
			t.Fatalf("identity field %s is missing", name)
		}
	}
	if _, ok := reflect.TypeFor[Target]().FieldByName("APIVersion"); ok {
		t.Fatal("v1 chat target must not allow classic API routing")
	}
}
