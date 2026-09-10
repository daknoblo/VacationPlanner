// Package foundry implements resource-bound Azure text chat using an explicit
// service principal. Other inference operations are deliberately unsupported.
package foundry

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Config separates environment-owned infrastructure from persisted selections.
type Config struct {
	ResourceID      string
	ImageResourceID string
	TenantID        string
	ClientID        string
	ClientSecret    string `json:"-"`
	Endpoint        string
	Deployment      string
	APIVersion      string
	Models          []string
}

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	groupPattern      = regexp.MustCompile(`^[a-zA-Z0-9_().-]{1,90}$`)
	accountPattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{1,63}$`)
	deploymentPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
)

// Requested intentionally ignores generic endpoint/model overrides.
func (c Config) Requested() bool {
	return c.ResourceID != "" || c.ImageResourceID != "" || c.TenantID != "" ||
		c.ClientID != "" || c.ClientSecret != ""
}

// Validate returns only fixed diagnostics, never configuration values.
func (c Config) Validate() error {
	if !c.Requested() {
		return nil
	}
	if !uuidPattern.MatchString(c.TenantID) || !uuidPattern.MatchString(c.ClientID) ||
		strings.TrimSpace(c.ClientSecret) == "" {
		return errors.New("foundry requires valid tenant/client UUIDs and a nonempty client secret")
	}
	if _, err := canonicalResourceID(c.ResourceID); err != nil {
		return err
	}
	if c.ImageResourceID != "" {
		if _, err := canonicalResourceID(c.ImageResourceID); err != nil {
			return errors.New("foundry image resource must be a complete Cognitive Services account resource ID")
		}
	}
	if c.Endpoint != "" {
		if _, err := NormalizeEndpoint(c.Endpoint); err != nil {
			return err
		}
		if c.APIVersion != "" && strings.TrimRight(c.Endpoint, "/") != rootEndpoint(c.Endpoint) {
			return errors.New("foundry classic API version requires a resource-root endpoint override")
		}
	}
	if c.Deployment != "" && !deploymentPattern.MatchString(c.Deployment) {
		return errors.New("foundry deployment name is invalid")
	}
	// Deliberately support one documented GA classic schema, separately from v1.
	if c.APIVersion != "" && c.APIVersion != "2024-10-21" {
		return errors.New("foundry API version must be empty for v1 or 2024-10-21 for classic chat")
	}
	for _, model := range c.Models {
		if model == "" || len(model) > 128 || strings.TrimSpace(model) != model ||
			strings.ContainsAny(model, "/\\?#\r\n\t") {
			return errors.New("foundry model allow-list contains an invalid entry")
		}
	}
	return nil
}

func canonicalResourceID(raw string) (string, error) {
	p := strings.Split(strings.TrimSuffix(raw, "/"), "/")
	if len(p) != 9 || p[0] != "" || !strings.EqualFold(p[1], "subscriptions") ||
		!uuidPattern.MatchString(p[2]) || !strings.EqualFold(p[3], "resourceGroups") ||
		!groupPattern.MatchString(p[4]) || strings.HasSuffix(p[4], ".") ||
		!strings.EqualFold(p[5], "providers") || !strings.EqualFold(p[6], "Microsoft.CognitiveServices") ||
		!strings.EqualFold(p[7], "accounts") || !accountPattern.MatchString(p[8]) {
		return "", errors.New("foundry resource must be a complete Cognitive Services account resource ID")
	}
	return strings.ToLower(strings.TrimSuffix(raw, "/")), nil
}

// NormalizeEndpoint accepts only unambiguous public Azure OpenAI resource roots
// or explicit v1 bases. Cognitive Services roots alone do not prove an API route.
func NormalizeEndpoint(raw string) (string, error) {
	u, err := parseEndpoint(raw)
	if err != nil {
		return "", err
	}
	name, suffix := azureHost(u.Hostname())
	if name == "" || (suffix == "cognitiveservices.azure.com" && u.Path != "/openai/v1") {
		return "", errors.New("foundry endpoint is not a supported Azure OpenAI inference endpoint")
	}
	u.Path = "/openai/v1"
	return u.String(), nil
}

func parseEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || raw != strings.TrimSpace(raw) || len(raw) > 2048 || u.Scheme != "https" ||
		u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawFragment != "" || u.Opaque != "" || strings.Contains(u.EscapedPath(), "%") ||
		strings.Contains(raw, "#") || (u.Host != u.Hostname() && u.Host != u.Hostname()+":443") ||
		(u.Path != "" && u.Path != "/" && u.Path != "/openai/v1" && u.Path != "/openai/v1/") {
		return nil, errors.New("foundry endpoint requires HTTPS without credentials, query, fragment or ambiguous paths")
	}
	u.Host = strings.ToLower(u.Hostname())
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

func azureHost(host string) (string, string) {
	for _, suffix := range []string{"openai.azure.com", "services.ai.azure.com", "cognitiveservices.azure.com"} {
		if name, ok := strings.CutSuffix(strings.ToLower(host), "."+suffix); ok && accountPattern.MatchString(name) {
			return name, suffix
		}
	}
	return "", ""
}

func rootEndpoint(endpoint string) string {
	return strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/openai/v1")
}

type accountProperties struct {
	Endpoint            string            `json:"endpoint"`
	Endpoints           map[string]string `json:"endpoints"`
	CustomSubDomainName string            `json:"customSubDomainName"`
}

func selectEndpoint(p accountProperties, override string) (string, error) {
	rawEndpoints := make([]string, 0, 1+len(p.Endpoints))
	rawEndpoints = append(rawEndpoints, p.Endpoint)
	for _, endpoint := range p.Endpoints {
		rawEndpoints = append(rawEndpoints, endpoint)
	}
	if override != "" {
		normalized, err := NormalizeEndpoint(override)
		if err != nil {
			return "", err
		}
		target, _ := url.Parse(normalized)
		name, _ := azureHost(target.Hostname())
		for _, raw := range rawEndpoints {
			u, err := parseEndpoint(raw)
			if err != nil {
				continue
			}
			otherName, _ := azureHost(u.Hostname())
			if target.Host == u.Host || (name == otherName && otherName != "" &&
				strings.EqualFold(name, p.CustomSubDomainName)) {
				return normalized, nil
			}
		}
		return "", errors.New("foundry endpoint override does not match account endpoint metadata")
	}
	candidates := make(map[string]bool)
	for _, raw := range rawEndpoints {
		if endpoint, err := NormalizeEndpoint(raw); err == nil {
			candidates[endpoint] = true
		}
	}
	if len(candidates) == 1 {
		for candidate := range candidates {
			return candidate, nil
		}
	}
	if len(candidates) > 1 {
		return "", errors.New("foundry account has ambiguous inference endpoints; configure a same-account override")
	}
	return "", errors.New("foundry account has no supported inference endpoint")
}
