package ai

import (
	"context"
	"encoding/json"
	"errors"
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
		client := New(backend)
		if !client.Enabled() {
			t.Fatal("explicit identity must enable AI")
		}
		got, err := client.Recommend(context.Background(), "saved-alias", RecommendInput{Destination: "Berlin"})
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

func TestFoundryResolutionFailureStopsGeneration(t *testing.T) {
	want := errors.New("invalid identity")
	backend := &fakeFoundry{err: want}
	_, err := New(backend).Recommend(context.Background(), "production-chat", RecommendInput{})
	if !errors.Is(err, want) || backend.calls != 0 {
		t.Fatalf("resolution failed but generation attempted: calls=%d err=%v", backend.calls, err)
	}
}

func TestFoundryProbeIsSmall(t *testing.T) {
	backend := &fakeFoundry{target: foundry.Target{Deployment: "production-chat"}}
	if err := New(backend).Probe(context.Background(), backend.target); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backend.payload), `"max_completion_tokens":256`) || backend.calls != 1 {
		t.Fatalf("probe must be a single bounded request: %s", backend.payload)
	}
	if err := New(nil).Probe(context.Background(), backend.target); err == nil {
		t.Fatal("disabled AI must not generate a probe")
	}
}

func TestFoundryActivitySuggestionsUseSameAdapter(t *testing.T) {
	backend := &fakeFoundry{
		target:   foundry.Target{Deployment: "production-chat"},
		response: `{"choices":[{"message":{"content":"{\"activities\":[{\"name\":\"Museum\"}]}"}}]}`,
	}
	activities, err := New(backend).SuggestActivities(context.Background(), "production-chat", "Berlin", "museum")
	if err != nil || len(activities) != 1 || activities[0].Name != "Museum" || backend.calls != 1 {
		t.Fatalf("activity adapter not preserved: %v, %v", activities, err)
	}
}

func TestFoundryResponseErrorsAreSanitized(t *testing.T) {
	for _, response := range []string{
		`{"error":{"message":"private-provider-details"}}`,
		`{"choices":[]}`,
		`{"choices":[{"message":{"content":""}}]}`,
		`invalid-private-provider-details`,
	} {
		backend := &fakeFoundry{target: foundry.Target{Deployment: "production-chat"}, response: response}
		_, err := New(backend).Recommend(context.Background(), "production-chat", RecommendInput{})
		if err == nil || strings.Contains(err.Error(), "private-provider-details") {
			t.Fatalf("provider error not safely reported: %v", err)
		}
	}
}
