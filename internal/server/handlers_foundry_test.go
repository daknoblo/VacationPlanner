package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/foundry"
)

type stubFoundry struct {
	refreshes int
	calls     int
	lastModel string
	fail      bool
}

func (f *stubFoundry) Refresh(context.Context) error {
	f.refreshes++
	if f.fail {
		return errors.New("ARM discovery returned HTTP 403")
	}
	return nil
}

func (*stubFoundry) Snapshots(context.Context) ([]foundry.Snapshot, error) {
	return []foundry.Snapshot{{
		Role: "main", ResourceID: "/main/account", Endpoint: "https://travel.openai.azure.com/openai/v1",
		UpdatedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Deployments: []foundry.Deployment{
			{Name: "production-chat", Model: "gpt-4o", ModelVersion: "2024-08-06", ModelFormat: "OpenAI", ProvisioningState: "Succeeded", ChatSupported: true},
			{Name: "<script>untrusted</script>", Model: "unsupported", Reason: "model is not supported by the text chat adapter"},
		},
	}}, nil
}

func (*stubFoundry) ResolveChat(_ context.Context, name string) (foundry.Target, error) {
	if name != "production-chat" && name != "next-chat" {
		return foundry.Target{}, errors.New("chat deployment not present in ARM catalog")
	}
	return foundry.Target{
		Endpoint: "https://travel.openai.azure.com/openai/v1", Deployment: name, SupportsTemperature: true,
	}, nil
}

func (f *stubFoundry) DoChat(_ context.Context, target foundry.Target, _ []byte) ([]byte, error) {
	f.calls++
	f.lastModel = target.Deployment
	if f.fail {
		return nil, errors.New("chat returned HTTP 403")
	}
	return []byte(`{"choices":[{"message":{"content":"OK"}}]}`), nil
}

func foundryTestServer(t *testing.T) (*Server, *stubFoundry) {
	t.Helper()
	s := newIntegrationServer(t)
	f := &stubFoundry{}
	s.cfg.Azure.ResourceID = "/subscriptions/11111111-1111-4111-8111-111111111111/resourceGroups/travel/providers/Microsoft.CognitiveServices/accounts/travel"
	s.cfg.Azure.ClientSecret = "private-test-secret"
	s.foundry = f
	s.ai = ai.NewFoundry(f)
	return s, f
}

func postAISettings(s *Server, path string, values url.Values, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	if csrf {
		req.Header.Set("X-CSRF-Token", s.newCSRFToken())
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestFoundrySettingsAndHealthNeverCallAzure(t *testing.T) {
	s, backend := foundryTestServer(t)
	ctx := context.Background()
	if err := s.store.PutSetting(ctx, s.foundrySettingKey("chat"), "missing-old-deployment"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/readyz", "/settings"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d: %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "private-test-secret") {
			t.Fatal("secret leaked to UI")
		}
		if strings.Contains(rec.Body.String(), "<script>untrusted</script>") {
			t.Fatal("metadata was rendered as HTML")
		}
		if path == "/settings" {
			for _, want := range []string{"missing-old-deployment", "not usable", "Metadata cached", "Not checked", "confirm_cost", "gpt-4o", "2024-08-06", "model is not supported"} {
				if !strings.Contains(rec.Body.String(), want) {
					t.Fatalf("settings missing %q", want)
				}
			}
		}
	}
	if backend.refreshes != 0 || backend.calls != 0 {
		t.Fatal("GET performed Azure work")
	}
}

func TestFoundrySettingsPreserveLegacyAndAccountSelections(t *testing.T) {
	s, _ := foundryTestServer(t)
	ctx := context.Background()
	if err := s.store.PutSetting(ctx, settingAIModel, "legacy-model"); err != nil {
		t.Fatal(err)
	}
	rec := postAISettings(s, "/settings/ai", url.Values{"model": {"production-chat"}, "base_url": {"https://foreign.invalid"}}, true)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "/settings" {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	originalKey := s.foundrySettingKey("chat")
	s.cfg.Azure.ResourceID = strings.ToUpper(s.cfg.Azure.ResourceID) + "/"
	if originalKey != s.foundrySettingKey("chat") {
		t.Fatal("account case or trailing slash changed selection binding")
	}
	s.cfg.Azure.ImageResourceID = "/unrelated/image"
	if originalKey != s.foundrySettingKey("chat") {
		t.Fatal("changing only image resource changed text selection binding")
	}
	s.cfg.Azure.ResourceID += "-other"
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.foundryDeployment(settings) != "" || settings[originalKey] != "production-chat" || settings[settingAIModel] != "legacy-model" {
		t.Fatal("selections leaked across accounts or overwrote legacy mode")
	}
}

func TestFoundryLockedDeploymentCannotOverwriteSavedSelection(t *testing.T) {
	s, _ := foundryTestServer(t)
	ctx := context.Background()
	if err := s.store.PutSetting(ctx, s.foundrySettingKey("chat"), "production-chat"); err != nil {
		t.Fatal(err)
	}
	s.cfg.Azure.Deployment = "next-chat"
	rec := postAISettings(s, "/settings/ai", url.Values{"model": {""}}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("locked save: %d", rec.Code)
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings[s.foundrySettingKey("chat")] != "production-chat" || s.foundryDeployment(settings) != "next-chat" {
		t.Fatal("locked selection lost its saved value or precedence")
	}
}

func TestFoundryProbeRequiresCSRFConsentAndUsesSavedSelection(t *testing.T) {
	s, backend := foundryTestServer(t)
	ctx := context.Background()
	if err := s.store.PutSetting(ctx, s.foundrySettingKey("chat"), "production-chat"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/settings/ai", "/settings/ai/discover", "/settings/ai/probe"} {
		if rec := postAISettings(s, path, url.Values{"confirm_cost": {"yes"}}, false); rec.Code != http.StatusForbidden {
			t.Fatalf("missing CSRF accepted: %s", path)
		}
	}
	postAISettings(s, "/settings/ai/probe", nil, true)
	if backend.calls != 0 {
		t.Fatal("probe without consent billed")
	}
	rec := postAISettings(s, "/settings/ai/probe", url.Values{"confirm_cost": {"yes"}, "model": {"attacker-choice"}}, true)
	if rec.Code != http.StatusOK || backend.calls != 1 || backend.lastModel != "production-chat" {
		t.Fatalf("probe didn't use saved selection: %d, %d, %s", rec.Code, backend.calls, backend.lastModel)
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.foundrySettings(ctx, settings)
	if err != nil || view.ProbeStatus != "settings.foundry.reachable" {
		t.Fatalf("missing persistent probe result: %v, %v", view, err)
	}
	s.cfg.Azure.Deployment = "next-chat"
	view, err = s.foundrySettings(ctx, settings)
	if err != nil || view.ProbeStatus != "settings.foundry.not_checked" {
		t.Fatalf("old probe authorized new selection: %v, %v", view, err)
	}
}

func TestFoundryDiscoveryDoesNotGenerateOrChangeSelection(t *testing.T) {
	s, backend := foundryTestServer(t)
	ctx := context.Background()
	if err := s.store.PutSetting(ctx, s.foundrySettingKey("chat"), "production-chat"); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	rec := postAISettings(s, "/settings/ai/discover", nil, true)
	if !strings.Contains(rec.Body.String(), "403") || backend.refreshes != 1 || backend.calls != 0 {
		t.Fatalf("discovery failure not surfaced: %d %s", rec.Code, rec.Body.String())
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil || settings[s.foundrySettingKey("chat")] != "production-chat" {
		t.Fatal("discovery changed selection")
	}
	postAISettings(s, "/settings/ai/probe", url.Values{"confirm_cost": {"yes"}}, true)
	settings, err = s.store.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var probe foundryProbeResult
	if err := json.Unmarshal([]byte(settings[s.foundrySettingKey("probe")]), &probe); err != nil || probe.Error == "" {
		t.Fatal("failed inference incorrectly persisted success")
	}
}

func TestLegacyAIEnvironmentOverridesPreserveSavedValues(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	for key, value := range map[string]string{settingAIBaseURL: "http://localhost:11434/v1", settingAIModel: "local-model", settingAIAPIVersion: ""} {
		if err := s.store.PutSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	s.cfg.Azure.Endpoint = "https://cloud.openai.azure.com"
	s.cfg.Azure.Deployment = "production-chat"
	s.cfg.Azure.APIVersion = "2024-10-21"
	rec := postAISettings(s, "/settings/ai", nil, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	endpoint, deployment, version, err := s.aiSettings(ctx)
	if err != nil || endpoint != s.cfg.Azure.Endpoint || deployment != s.cfg.Azure.Deployment || version != s.cfg.Azure.APIVersion {
		t.Fatal("ENV precedence lost")
	}
	s.cfg.Azure.Endpoint, s.cfg.Azure.Deployment, s.cfg.Azure.APIVersion = "", "", ""
	endpoint, deployment, version, err = s.aiSettings(ctx)
	if err != nil || endpoint != "http://localhost:11434/v1" || deployment != "local-model" || version != "" {
		t.Fatal("locked form cleared underlying values")
	}
}

func TestServerWiresExplicitIdentityWithoutNetworkOrKeyFallback(t *testing.T) {
	s := newIntegrationServer(t)
	s.cfg.Azure = foundry.Config{
		ResourceID:   "/subscriptions/11111111-1111-4111-8111-111111111111/resourceGroups/travel/providers/Microsoft.CognitiveServices/accounts/travel",
		TenantID:     "22222222-2222-4222-8222-222222222222",
		ClientID:     "33333333-3333-4333-8333-333333333333",
		ClientSecret: "private-test-secret",
	}
	configured, err := New(s.cfg, s.log, s.logs, s.store, ai.New("legacy-key"))
	if err != nil {
		t.Fatal(err)
	}
	if configured.foundry == nil || !configured.ai.Enabled() {
		t.Fatal("explicit identity not wired into existing AI features")
	}
	rec := httptest.NewRecorder()
	configured.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private-test-secret") {
		t.Fatalf("cold Foundry settings failed or leaked secret: %d", rec.Code)
	}
	_, err = configured.ai.Recommend(context.Background(), "https://must-not-be-used.invalid", "missing-deployment", "", ai.RecommendInput{})
	if err == nil {
		t.Fatal("undiscovered deployment must fail without reaching legacy endpoint")
	}
	s.cfg.Azure.ClientSecret = ""
	if _, err := New(s.cfg, s.log, s.logs, s.store, ai.New("legacy-key")); err == nil {
		t.Fatal("incomplete identity silently fell back to key")
	}
}

func TestStartupDiscoveryIsJoinedWithoutGeneration(t *testing.T) {
	s, backend := foundryTestServer(t)
	stop := s.StartAIDiscovery(context.Background())
	stop()
	if backend.refreshes != 1 || backend.calls != 0 {
		t.Fatal("startup must only read metadata once")
	}
	s.foundry = nil
	s.StartAIDiscovery(context.Background())()
}
