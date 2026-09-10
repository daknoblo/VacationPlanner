package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/foundry"
)

type fakeFoundry struct {
	target   foundry.Target
	err      error
	selected string
	payload  []byte
	calls    int
	response string
}

func (f *fakeFoundry) ResolveChat(_ context.Context, saved string) (foundry.Target, error) {
	f.selected = saved
	return f.target, f.err
}

func (f *fakeFoundry) DoChat(_ context.Context, _ foundry.Target, payload []byte) ([]byte, error) {
	f.calls++
	f.payload = payload
	if f.response != "" {
		return []byte(f.response), f.err
	}
	return []byte(`{"choices":[{"message":{"content":"{\"suggestions\":[{\"name\":\"Museum\"}]}"}}]}`), f.err
}

func TestFoundryRecommendationsUseDeploymentAndSupportedParameters(t *testing.T) {
	for _, supportsTemperature := range []bool{true, false} {
		backend := &fakeFoundry{target: foundry.Target{Deployment: "production-chat", SupportsTemperature: supportsTemperature}}
		client := NewFoundry(backend)
		if !client.Enabled() {
			t.Fatal("explicit identity must enable AI without a key")
		}
		got, err := client.Recommend(context.Background(), "https://ignored.invalid", "saved-alias", "ignored-version", RecommendInput{Destination: "Berlin"})
		if err != nil || len(got) != 1 || got[0].Name != "Museum" {
			t.Fatalf("Recommend = %v, %v", got, err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(backend.payload, &payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["model"]) != `"production-chat"` || backend.selected != "saved-alias" {
			t.Fatalf("not using resolved alias: %s", backend.payload)
		}
		if _, ok := payload["temperature"]; ok != supportsTemperature {
			t.Fatalf("unsupported temperature: %s", backend.payload)
		}
		if string(payload["max_completion_tokens"]) != "8192" {
			t.Fatalf("unbounded recommendation: %s", backend.payload)
		}
	}
}

func TestFoundryResolutionFailureNeverFallsBack(t *testing.T) {
	want := errors.New("invalid identity")
	backend := &fakeFoundry{err: want}
	client := NewFoundry(backend)
	client.apiKey = "must-not-be-used"
	_, err := client.Recommend(context.Background(), "https://must-not-be-called.invalid", "", "", RecommendInput{})
	if !errors.Is(err, want) || backend.calls != 0 {
		t.Fatalf("unexpected fallback: calls=%d err=%v", backend.calls, err)
	}
}

func TestFoundryProbeIsSmall(t *testing.T) {
	backend := &fakeFoundry{target: foundry.Target{Deployment: "production-chat"}}
	if err := NewFoundry(backend).Probe(context.Background(), backend.target); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backend.payload), `"max_completion_tokens":256`) || backend.calls != 1 {
		t.Fatalf("probe must be a single bounded request: %s", backend.payload)
	}
	if err := New("key").Probe(context.Background(), backend.target); err == nil {
		t.Fatal("key mode must not masquerade as Foundry")
	}
}

func TestFoundryActivitySuggestionsUseSameAdapter(t *testing.T) {
	backend := &fakeFoundry{
		target:   foundry.Target{Deployment: "production-chat"},
		response: `{"choices":[{"message":{"content":"{\"activities\":[{\"name\":\"Museum\"}]}"}}]}`,
	}
	activities, err := NewFoundry(backend).SuggestActivities(context.Background(), "", "production-chat", "", "Berlin", "museum")
	if err != nil || len(activities) != 1 || activities[0].Name != "Museum" || backend.calls != 1 {
		t.Fatalf("activity adapter not preserved: %v, %v", activities, err)
	}
}

func TestLegacyRecommendationSuccess(t *testing.T) {
	for _, version := range []string{"", "2024-10-21"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("legacy authorization changed")
			}
			if version != "" && r.Header.Get("api-key") != "test-key" {
				t.Error("classic Azure header missing")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"suggestions\":[{\"name\":\"Museum\"}]}"}}]}`))
		}))
		result, err := New("test-key").Recommend(context.Background(), srv.URL, "production-chat", version, RecommendInput{Destination: "Berlin"})
		srv.Close()
		if err != nil || len(result) != 1 || result[0].Name != "Museum" {
			t.Fatalf("legacy request failed: %v, %v", result, err)
		}
	}
}

func TestLegacyChatModesAndRedirects(t *testing.T) {
	for _, version := range []string{"", "2024-10-21"} {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("legacy bearer missing")
			}
			wantPath := "/chat/completions"
			if version != "" {
				wantPath = "/openai/deployments/production-chat/chat/completions"
				if r.Header.Get("api-key") != "test-key" || r.URL.Query().Get("api-version") != version {
					t.Error("classic Azure request changed")
				}
			} else if r.Header.Get("api-key") != "" {
				t.Error("unexpected classic header")
			}
			if r.URL.Path != wantPath {
				t.Errorf("path = %s", r.URL.Path)
			}
			http.Redirect(w, r, "/credential-sink", http.StatusTemporaryRedirect)
		}))
		_, err := New("test-key").doChat(context.Background(), srv.URL, "production-chat", version, []chatMessage{{Role: "user", Content: "Hello"}}, 0.5)
		srv.Close()
		if err == nil || requests != 1 {
			t.Fatalf("redirect followed or reported success: %d, %v", requests, err)
		}
	}
}
