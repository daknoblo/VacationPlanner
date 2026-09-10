package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type foundryConnection interface {
	ai.FoundryBackend
	Refresh(context.Context) error
	Snapshots(context.Context) ([]foundry.Snapshot, error)
}

type foundrySettingsView struct {
	Deployment     string
	Endpoint       string
	APIVersion     string
	ModelLocked    bool
	Models         string
	SelectionError string
	Catalogs       []foundry.Snapshot
	ProbeStatus    string
	ProbeError     string
	ProbeAt        string
	ActionError    string
}

type foundryProbeResult struct {
	Target string    `json:"target"`
	At     time.Time `json:"at"`
	Error  string    `json:"error,omitempty"`
}

// StartAIDiscovery refreshes metadata once without blocking HTTP startup.
// The returned function cancels and joins the work before the store is closed.
func (s *Server) StartAIDiscovery(ctx context.Context) func() {
	if s.foundry == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.foundry.Refresh(ctx); err != nil {
			s.log.Warn("startup AI discovery failed", "err", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Server) foundrySettingKey(kind string) string {
	id := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s.cfg.Azure.ResourceID), "/"))
	return fmt.Sprintf("ai.foundry.%s.%x", kind, sha256.Sum256([]byte(id)))
}

func (s *Server) foundryDeployment(settings map[string]string) string {
	if s.cfg.Azure.Deployment != "" {
		return s.cfg.Azure.Deployment
	}
	return settings[s.foundrySettingKey("chat")]
}

func chatTargetFingerprint(target foundry.Target) (string, error) {
	data, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("encoding chat target: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (s *Server) foundrySettings(ctx context.Context, settings map[string]string) (*foundrySettingsView, error) {
	if s.foundry == nil {
		return nil, nil
	}
	catalogs, err := s.foundry.Snapshots(ctx)
	if err != nil {
		return nil, err
	}
	view := &foundrySettingsView{
		Deployment: s.foundryDeployment(settings), ModelLocked: s.cfg.Azure.Deployment != "",
		Models:     strings.Join(s.cfg.Azure.Models, ", "),
		APIVersion: s.cfg.Azure.APIVersion, Catalogs: catalogs,
		ProbeStatus: "settings.foundry.not_checked",
	}
	if view.Deployment == "" {
		view.ProbeStatus = "settings.foundry.not_configured"
	}
	if len(catalogs) > 0 {
		view.Endpoint = catalogs[0].Endpoint
	}
	if s.cfg.Azure.Endpoint != "" {
		view.Endpoint = s.cfg.Azure.Endpoint
	}
	target, err := s.foundry.ResolveChat(ctx, view.Deployment)
	if err != nil {
		view.SelectionError = err.Error()
		return view, nil //nolint:nilerr // Invalid selections are shown in Settings, not hidden behind HTTP 500.
	}
	view.Endpoint = target.Endpoint
	fingerprint, err := chatTargetFingerprint(target)
	if err != nil {
		return nil, err
	}
	if raw := settings[s.foundrySettingKey("probe")]; raw != "" {
		var probe foundryProbeResult
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			return nil, fmt.Errorf("reading saved AI probe: %w", err)
		}
		if probe.Target == fingerprint {
			view.ProbeAt = probe.At.UTC().Format(time.RFC3339)
			view.ProbeError = probe.Error
			view.ProbeStatus = "settings.foundry.reachable"
			if probe.Error != "" {
				view.ProbeStatus = "settings.foundry.failed"
			}
		}
	}
	return view, nil
}

func (s *Server) handleUpdateFoundrySettings(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Azure.Deployment != "" {
		// A locked override must not overwrite the saved selection underneath it.
		s.redirectSettings(w, r)
		return
	}
	deployment := formStr(r, "model")
	if !maxLen(deployment, 200) {
		s.formError(w, r, "#ai-settings-error", i18n.FromContext(r.Context()).T("error.input_toolong"))
		return
	}
	if deployment != "" {
		if _, err := s.foundry.ResolveChat(r.Context(), deployment); err != nil {
			s.formError(w, r, "#ai-settings-error", i18n.FromContext(r.Context()).T("settings.foundry.selection_error")+": "+err.Error())
			return
		}
	}
	if err := s.putSetting(r.Context(), s.foundrySettingKey("chat"), deployment); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

func (s *Server) handleFoundryDiscover(w http.ResponseWriter, r *http.Request) {
	if s.foundry == nil {
		s.formError(w, r, "#ai-settings-error", i18n.FromContext(r.Context()).T("settings.foundry.not_configured"))
		return
	}
	if err := s.foundry.Refresh(r.Context()); err != nil {
		s.log.Warn("AI discovery failed", "err", err)
		s.foundryDiscoveryError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

func (s *Server) foundryDiscoveryError(w http.ResponseWriter, r *http.Request, discoveryErr error) {
	message := i18n.FromContext(r.Context()).T("settings.foundry.discovery_error") + ": " + discoveryErr.Error()
	if !isHTMX(r) {
		s.formError(w, r, "#ai-settings-error", message)
		return
	}
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.foundrySettings(r.Context(), settings)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view.ActionError = message
	w.Header().Set("HX-Retarget", "#foundry-settings-panel")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.fragment(w, r, "foundry_settings", viewData{
		CSRFToken: csrfToken(r.Context()),
		Data:      map[string]any{"Foundry": view},
	})
}

func (s *Server) handleFoundryProbe(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	if s.foundry == nil {
		s.formError(w, r, "#ai-settings-error", loc.T("settings.foundry.not_configured"))
		return
	}
	if formStr(r, "confirm_cost") != "yes" {
		s.formError(w, r, "#ai-settings-error", loc.T("settings.foundry.confirm_required"))
		return
	}
	settings, err := s.settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	deployment := s.foundryDeployment(settings)
	target, err := s.foundry.ResolveChat(r.Context(), deployment)
	if err != nil {
		s.formError(w, r, "#ai-settings-error", loc.T("settings.foundry.selection_error")+": "+err.Error())
		return
	}
	fingerprint, err := chatTargetFingerprint(target)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	probe := foundryProbeResult{Target: fingerprint, At: time.Now().UTC()}
	if err := s.ai.Probe(r.Context(), target); err != nil {
		probe.Error = err.Error()
		s.log.Warn("AI text probe failed", "err", err)
	}
	data, err := json.Marshal(probe)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.putSetting(r.Context(), s.foundrySettingKey("probe"), string(data)); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}
