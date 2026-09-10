package foundry

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func realisticAccountProperties() accountProperties {
	return accountProperties{
		Endpoint:            "https://main.cognitiveservices.azure.com/",
		CustomSubDomainName: "main",
		Endpoints: map[string]string{
			"Azure OpenAI Legacy API - Latest moniker": "https://main.openai.azure.com/",
			"Azure AI Model Inference API":             "https://main.services.ai.azure.com/models",
			"Azure AI Services":                        "https://main.services.ai.azure.com/",
			"Speech Services":                          "https://eastus.stt.speech.microsoft.com/",
			"Computer Vision":                          "https://main.cognitiveservices.azure.com/",
		},
	}
}

func TestAutomaticEndpointPreference(t *testing.T) {
	for _, tt := range []struct {
		name       string
		properties accountProperties
		want       string
	}{
		{"multiservice account", realisticAccountProperties(), mainEndpoint},
		{"openai primary", accountProperties{Endpoint: "https://main.openai.azure.com/"}, mainEndpoint},
		{"portal v1 primary", accountProperties{Endpoint: mainEndpoint + "/"}, mainEndpoint},
		{"services primary", accountProperties{Endpoint: "https://main.services.ai.azure.com/"}, "https://main.services.ai.azure.com/openai/v1"},
		{"cognitive explicit v1", accountProperties{Endpoint: "https://main.cognitiveservices.azure.com/openai/v1/"}, "https://main.cognitiveservices.azure.com/openai/v1"},
		{"map-only same-account aliases", accountProperties{Endpoints: map[string]string{
			"First":  "https://main.services.ai.azure.com/",
			"Second": mainEndpoint,
		}}, mainEndpoint},
		{"services primary with openai alias", accountProperties{
			Endpoint:  "https://main.services.ai.azure.com/",
			Endpoints: map[string]string{"additional": mainEndpoint},
		}, mainEndpoint},
		{"named OpenAI API", accountProperties{
			Endpoint:  "https://main.cognitiveservices.azure.com/",
			Endpoints: map[string]string{"Azure OpenAI API": mainEndpoint + "/"},
		}, mainEndpoint},
		{"legacy key preference", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"Azure OpenAI Legacy API - Latest moniker": "https://main.services.ai.azure.com/openai/v1"},
		}, "https://main.services.ai.azure.com/openai/v1"},
		{"regional non-inference root", accountProperties{
			Endpoint:  "https://eastus.api.cognitive.microsoft.com/",
			Endpoints: map[string]string{"Azure OpenAI API": mainEndpoint},
		}, mainEndpoint},
		{"custom domain differs from ARM account name", accountProperties{
			Endpoint:            "https://custom-name.cognitiveservices.azure.com/",
			CustomSubDomainName: "custom-name",
			Endpoints:           map[string]string{"Azure OpenAI API": "https://custom-name.openai.azure.com/"},
		}, "https://custom-name.openai.azure.com/openai/v1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for range 50 {
				got, err := selectEndpoint(tt.properties)
				if err != nil || got != tt.want {
					t.Fatalf("automatic endpoint = %q, error = %v; want %q", got, err, tt.want)
				}
			}
		})
	}
}

func TestAutomaticEndpointRejectsUnsafeOrUnprovenMetadata(t *testing.T) {
	for _, tt := range []struct {
		name       string
		properties accountProperties
		diagnostic string
	}{
		{"empty metadata", accountProperties{}, errEndpointMissing},
		{"cognitive root cannot invent OpenAI", accountProperties{
			Endpoint: "https://main.cognitiveservices.azure.com/", CustomSubDomainName: "main",
		}, errEndpointMissing},
		{"custom subdomain alone cannot invent OpenAI", accountProperties{CustomSubDomainName: "main"}, errEndpointMissing},
		{"conflicting named account", accountProperties{
			Endpoint:  "https://main.cognitiveservices.azure.com/",
			Endpoints: map[string]string{"Azure OpenAI Legacy API - Latest moniker": imageEndpoint},
		}, errEndpointAmbiguous},
		{"conflicting fallback account", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"Azure AI Services": imageEndpoint},
		}, errEndpointAmbiguous},
		{"conflicting map-only accounts", accountProperties{Endpoints: map[string]string{
			"one": mainEndpoint, "two": imageEndpoint,
		}}, errEndpointAmbiguous},
		{"custom subdomain mismatch", accountProperties{
			Endpoint: mainEndpoint, CustomSubDomainName: "images",
		}, errEndpointAmbiguous},
		{"foreign named endpoint", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"Azure OpenAI API": "https://attacker.example/openai/v1"},
		}, errEndpointInvalid},
		{"malformed named endpoint cannot be ignored", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"Azure OpenAI API": "https://main.openai.azure.com/openai//v1"},
		}, errEndpointInvalid},
		{"malformed unnamed openai endpoint", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"additional": "https://main.openai.azure.com/openai%2fv1"},
		}, errEndpointInvalid},
		{"insecure advertised endpoint", accountProperties{
			Endpoint:  mainEndpoint,
			Endpoints: map[string]string{"additional": "http://main.services.ai.azure.com/openai/v1"},
		}, errEndpointInvalid},
		{"query on endpoint", accountProperties{
			Endpoint: mainEndpoint + "?secret=private-diagnostic-value",
		}, errEndpointInvalid},
		{"generic root mislabeled OpenAI", accountProperties{
			Endpoint:  "https://main.cognitiveservices.azure.com/",
			Endpoints: map[string]string{"Azure OpenAI API": "https://main.cognitiveservices.azure.com/"},
		}, errEndpointInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for range 30 {
				got, err := selectEndpoint(tt.properties)
				if err == nil || got != "" || err.Error() != tt.diagnostic {
					t.Fatalf("unsafe metadata: got=%q error=%v want=%s", got, err, tt.diagnostic)
				}
			}
		})
	}
}

func TestAutomaticDiscoveryAndOldCacheRevalidation(t *testing.T) {
	c, cache, credential := testClient(t, testConfig())
	account, err := json.Marshal(map[string]any{
		"id": mainResource, "kind": "AIServices", "properties": realisticAccountProperties(),
	})
	if err != nil {
		t.Fatal(err)
	}
	setDiscovery(c, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == mainResource {
			return response(200, string(account)), nil
		}
		return response(200, deploymentsJSON(mainResource, "production", "gpt-6-astra", "")), nil
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	target, err := c.ResolveChat(context.Background(), "production")
	if err != nil || target.Endpoint != mainEndpoint || target.Model != "gpt-6-astra" {
		t.Fatalf("realistic account not resolved: %+v %v", target, err)
	}
	key := cacheKey(mainResource, "catalog")
	var legacy cachedCatalog
	if err := json.Unmarshal([]byte(cache.values[key]), &legacy); err != nil {
		t.Fatal(err)
	}
	// Old snapshots can have chosen an equivalent services alias or a classic
	// root. The stored account metadata, not that old URL, authorizes the base.
	legacy.Endpoint = "https://main.services.ai.azure.com"
	legacy.UpdatedAt = time.Now().UTC().Add(-time.Hour)
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	cache.values[key] = string(raw)
	restarted, err := New(testConfig(), cache)
	if err != nil {
		t.Fatal(err)
	}
	restarted.credential = credential
	before := len(credential.scopes)
	target, err = restarted.ResolveChat(context.Background(), "production")
	if err != nil || target.Endpoint != mainEndpoint || len(credential.scopes) != before {
		t.Fatalf("cached endpoints were not safely re-resolved without network: %+v %v", target, err)
	}
	if _, err := restarted.ResolveChat(context.Background(), ""); err == nil {
		t.Fatal("cache reload selected a deployment automatically")
	}
	legacy.Account.Endpoints["other"] = imageEndpoint
	raw, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	cache.values[key] = string(raw)
	if _, err := restarted.ResolveChat(context.Background(), "production"); err == nil {
		t.Fatal("conflicting account metadata authorized a cached target")
	}
}

func TestEndpointDiagnosticsPersistWithoutInference(t *testing.T) {
	for _, properties := range []accountProperties{
		{Endpoint: "https://main.cognitiveservices.azure.com/"},
		{Endpoint: mainEndpoint, Endpoints: map[string]string{"other": imageEndpoint}},
		{Endpoint: mainEndpoint + "?secret=private-diagnostic-value"},
	} {
		c, _, credential := testClient(t, testConfig())
		body, err := json.Marshal(map[string]any{"id": mainResource, "properties": properties})
		if err != nil {
			t.Fatal(err)
		}
		setDiscovery(c, func(*http.Request) (*http.Response, error) { return response(200, string(body)), nil })
		_, wantErr := selectEndpoint(properties)
		if err := c.Refresh(context.Background()); err == nil || wantErr == nil || !strings.Contains(err.Error(), wantErr.Error()) {
			t.Fatalf("specific endpoint diagnosis was masked: %v", err)
		}
		snapshots, err := c.Snapshots(context.Background())
		if err != nil || snapshots[0].Error != wantErr.Error() || !snapshots[0].Stale || len(credential.scopes) != 1 {
			t.Fatalf("endpoint error not persisted before further calls: %+v %v", snapshots, err)
		}
		if _, err := c.ResolveChat(context.Background(), "production"); err == nil {
			t.Fatal("unproven endpoint authorized inference")
		}
	}
}
