package foundry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Cache contains only non-secret settings. Existing SQLite settings implement it.
type Cache interface {
	GetSettings(context.Context) (map[string]string, error)
	PutSetting(context.Context, string, string) error
}

type Deployment struct {
	Name                string            `json:"name"`
	ResourceID          string            `json:"resource_id"`
	Model               string            `json:"model"`
	ModelVersion        string            `json:"model_version"`
	ModelFormat         string            `json:"model_format"`
	ProvisioningState   string            `json:"provisioning_state"`
	SKU                 map[string]any    `json:"sku"`
	Capabilities        map[string]string `json:"capabilities"`
	ChatSupported       bool              `json:"chat_supported"`
	SupportsTemperature bool              `json:"supports_temperature"`
	Reason              string            `json:"reason"`
}

type Snapshot struct {
	ResourceID  string       `json:"resource_id"`
	Endpoint    string       `json:"endpoint"`
	Deployments []Deployment `json:"deployments"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Error       string       `json:"error,omitempty"`
	Stale       bool         `json:"stale"`
	Role        string       `json:"role"`
}

type cachedCatalog struct {
	Snapshot
	Account accountProperties `json:"account"`
}

type refreshStatus struct {
	Failed bool      `json:"failed"`
	Error  string    `json:"error"`
	At     time.Time `json:"at"`
}

func cacheKey(resource, kind string) string {
	hash := sha256.Sum256([]byte(resource))
	return "foundry." + hex.EncodeToString(hash[:]) + "." + kind
}

// Snapshots returns main and (when different) image metadata independently.
// Cached metadata never establishes inference reachability.
func (c *Client) Snapshots(ctx context.Context) ([]Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	settings, err := c.cache.GetSettings(ctx)
	if err != nil {
		return nil, errors.New("foundry cached metadata could not be read")
	}
	result := make([]Snapshot, 0, len(c.resources()))
	for i, resource := range c.resources() {
		role := "main"
		if i > 0 {
			role = "image"
		}
		s := Snapshot{ResourceID: resource, Role: role, Stale: true}
		if raw := settings[cacheKey(resource, "catalog")]; raw != "" {
			var cached cachedCatalog
			if len(raw) > maxDiscoveryBytes || json.Unmarshal([]byte(raw), &cached) != nil ||
				cached.ResourceID != resource || cached.UpdatedAt.IsZero() ||
				!validateCached(&cached) {
				s.Error = "foundry cached catalog is invalid; refresh metadata"
			} else {
				s = cached.Snapshot
				s.Role = role
				s.Error = ""
				s.Stale = time.Since(s.UpdatedAt) > 24*time.Hour
				for j := range s.Deployments {
					classify(&s.Deployments[j])
				}
			}
		}
		if raw := settings[cacheKey(resource, "status")]; raw != "" {
			var status refreshStatus
			if len(raw) > 4096 || json.Unmarshal([]byte(raw), &status) != nil {
				s.Error = "foundry cached refresh status is invalid"
				s.Stale = true
			} else if status.Failed {
				// Status values are closed diagnostics; never render arbitrary cache text.
				if s.Error == "" {
					s.Error = safeCachedError(status.Error)
				}
				s.Stale = true
			}
		}
		if err := c.cacheErrors[resource]; err != "" {
			s.Error = err
			s.Stale = true
		}
		result = append(result, s)
	}
	return result, nil
}

func validateCached(cached *cachedCatalog) bool {
	endpoint, err := selectEndpoint(cached.Account)
	if err != nil || len(cached.Deployments) > maxDeployments {
		return false
	}
	cached.Endpoint = endpoint
	names := make(map[string]bool)
	for _, d := range cached.Deployments {
		name := strings.ToLower(d.Name)
		if !validDeployment(d, cached.ResourceID) || names[name] {
			return false
		}
		names[name] = true
	}
	return true
}

// Target is revalidated against the configured resource and catalog on every call.
type Target struct {
	ResourceID          string
	Endpoint            string
	Deployment          string
	Model               string
	ModelVersion        string
	SupportsTemperature bool
}

func (c *Client) ResolveChat(ctx context.Context, savedDeployment string) (Target, error) {
	name := savedDeployment
	if name == "" {
		return Target{}, errors.New("foundry chat deployment is not selected")
	}
	snapshots, err := c.Snapshots(ctx)
	if err != nil {
		return Target{}, err
	}
	s := snapshots[0]
	if s.Endpoint == "" || s.UpdatedAt.IsZero() {
		return Target{}, errors.New("foundry main resource metadata is unavailable; refresh discovery")
	}
	for _, d := range s.Deployments {
		if d.Name != name {
			continue
		}
		if !d.ChatSupported {
			return Target{}, errors.New("foundry selected deployment does not support the text chat adapter")
		}
		return Target{
			ResourceID: s.ResourceID, Endpoint: s.Endpoint, Deployment: d.Name,
			Model: d.Model, ModelVersion: d.ModelVersion,
			SupportsTemperature: d.SupportsTemperature,
		}, nil
	}
	return Target{}, errors.New("foundry selected chat deployment is absent from the main resource catalog")
}

func (c *Client) ValidateSelection(ctx context.Context, name string) error {
	_, err := c.ResolveChat(ctx, name)
	return err
}

// Only reviewed text Chat Completions models; future names and specialized
// APIs must not become supported merely by matching a family prefix.
var chatModelPattern = regexp.MustCompile(`^(gpt-4o(-mini)?|gpt-4\.1(-mini|-nano)?|gpt-5(-mini|-nano|-chat)?|gpt-5\.[12](-chat)?|gpt-5\.3-chat|gpt-5\.4(-mini|-nano)?|gpt-5\.5|gpt-5\.6-(sol|terra|luna)|gpt-6-astra|gpt-chat-latest|o1|o3(-mini)?|o4-mini)(-[0-9]{4}-[0-9]{2}-[0-9]{2})?$`)

func classify(d *Deployment) {
	d.ChatSupported, d.SupportsTemperature = false, false
	d.Reason = "model is not supported by the text chat adapter"
	if !strings.EqualFold(d.ProvisioningState, "Succeeded") {
		d.Reason = "deployment provisioning has not succeeded"
		return
	}
	sku, _ := d.SKU["name"].(string)
	if strings.Contains(strings.ToLower(sku), "batch") {
		d.Reason = "batch deployments do not support synchronous text chat"
		return
	}
	if !strings.EqualFold(d.ModelFormat, "OpenAI") || !chatModelPattern.MatchString(strings.ToLower(d.Model)) {
		return
	}
	for _, token := range strings.FieldsFunc(strings.ToLower(d.ModelVersion), func(r rune) bool { return r == '-' || r == '_' }) {
		if slices.Contains([]string{"realtime", "audio", "transcribe", "whisper", "tts", "sora", "batch"}, token) {
			d.Reason = "model version requires an unsupported inference API"
			return
		}
	}
	for k, value := range d.Capabilities {
		key := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(k))
		v := strings.ToLower(strings.TrimSpace(value))
		switch key {
		case "chatcompletion", "chatcompletions":
			if v != "true" && v != "1" {
				d.Reason = "chat completions capability is disabled or unrecognized"
				return
			}
		case "batchonly":
			if v != "false" && v != "0" {
				d.Reason = "deployment is batch-only or has unrecognized capabilities"
				return
			}
		case "protocol", "inferenceprotocol":
			if v != "openai" && v != "openai-compatible" && v != "openai_v1" && v != "openai-v1" {
				d.Reason = "deployment requires an unsupported inference protocol"
				return
			}
		}
	}
	d.ChatSupported = true
	d.SupportsTemperature = strings.HasPrefix(strings.ToLower(d.Model), "gpt-4")
	d.Reason = ""
}

func validDeployment(d Deployment, resource string) bool {
	return d.ResourceID == resource && deploymentPattern.MatchString(d.Name) &&
		d.Model != "" && len(d.Model) <= 128 && d.ModelFormat != "" &&
		d.ProvisioningState != "" && len(d.Capabilities) <= 128
}
