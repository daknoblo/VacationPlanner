package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSuggestionsObject(t *testing.T) {
	in := `{"suggestions":[{"name":"Castelo","category":"Burg","description":"d","reason":"r"}]}`
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Castelo" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestParseSuggestionsArray(t *testing.T) {
	in := `[{"name":"A"},{"name":"B"}]`
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestParseSuggestionsFenced(t *testing.T) {
	in := "```json\n{\"suggestions\":[{\"name\":\"A\"}]}\n```"
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestParseSuggestionsInvalid(t *testing.T) {
	if _, err := parseSuggestions("totally not json"); err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestParseSuggestionsEmbedded(t *testing.T) {
	in := "Sure! Here are some ideas for you:\n{\"suggestions\":[{\"name\":\"Sé\"}]}\nHope that helps!"
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Sé" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestClientDisabled(t *testing.T) {
	c := New("")
	if c.Enabled() {
		t.Fatal("client without API key must be disabled")
	}
}

func TestBuildEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		model   string
		apiVer  string
		want    string
	}{
		{
			name:    "openai compatible",
			baseURL: "https://api.openai.com/v1",
			model:   "gpt-4o-mini",
			want:    "https://api.openai.com/v1/chat/completions",
		},
		{
			name:    "azure adds deployment path",
			baseURL: "https://ddf6-msfoundry.openai.azure.com",
			model:   "model-router",
			apiVer:  "2025-01-01-preview",
			want:    "https://ddf6-msfoundry.openai.azure.com/openai/deployments/model-router/chat/completions?api-version=2025-01-01-preview",
		},
		{
			name:    "azure base already has deployment",
			baseURL: "https://x.openai.azure.com/openai/deployments/dep",
			model:   "dep",
			apiVer:  "2024-02-15-preview",
			want:    "https://x.openai.azure.com/openai/deployments/dep/chat/completions?api-version=2024-02-15-preview",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildEndpoint(tc.baseURL, tc.model, tc.apiVer); got != tc.want {
				t.Fatalf("buildEndpoint = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDoChatErrorSurfacesEndpointAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"The model gpt-x does not exist"}}`))
	}))
	defer srv.Close()

	c := New("test-key")
	_, err := c.doChat(context.Background(), srv.URL, "gpt-x", "",
		[]chatMessage{{Role: "user", Content: "hi"}}, 0.5)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	msg := err.Error()
	for _, want := range []string{"404", "gpt-x", "does not exist", srv.URL} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q should contain %q", msg, want)
		}
	}
}
