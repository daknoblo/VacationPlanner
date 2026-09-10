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
}

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	groupPattern      = regexp.MustCompile(`^[a-zA-Z0-9_().-]{1,90}$`)
	accountPattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{1,63}$`)
	deploymentPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
)

// Requested distinguishes disabled AI from a partial identity configuration.
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

type accountProperties struct {
	Endpoint            string            `json:"endpoint"`
	Endpoints           map[string]string `json:"endpoints"`
	CustomSubDomainName string            `json:"customSubDomainName"`
}

// selectEndpoint uses advertised URLs only. The account's Azure subdomain,
// corroborated across metadata, binds equivalent OpenAI/Foundry host aliases.
// Unrelated service endpoints (speech, vision, model-inference /models, etc.)
// aren't Chat Completions candidates and don't authorize an inference URL.
func selectEndpoint(p accountProperties) (string, error) {
	accountName := strings.ToLower(p.CustomSubDomainName)
	if accountName != "" && !accountPattern.MatchString(accountName) {
		return "", errors.New(errEndpointInvalid)
	}
	type candidate struct {
		endpoint string
		priority int
	}
	candidates := make([]candidate, 0, 1+len(p.Endpoints))
	inspect := func(raw, key string, primary bool) error {
		if raw == "" {
			return nil
		}
		namedOpenAI := strings.Contains(strings.ToLower(key), "openai")
		parsed, err := url.Parse(raw)
		if err != nil {
			if primary || namedOpenAI {
				return errors.New(errEndpointInvalid)
			}
			return nil
		}
		name, suffix := azureHost(parsed.Hostname())
		// A recognized Azure host with a root or v1 path is an API candidate.
		// Other paths are unrelated APIs unless explicitly labeled OpenAI.
		route := parsed.Path == "" || parsed.Path == "/" || parsed.Path == "/openai/v1" || parsed.Path == "/openai/v1/"
		openAIPath := strings.HasPrefix(parsed.Path, "/openai")
		if !primary && !namedOpenAI && suffix != "openai.azure.com" && !openAIPath && (name == "" || !route) {
			return nil
		}
		u, err := parseEndpoint(raw)
		if err != nil || (namedOpenAI && name == "") {
			return errors.New(errEndpointInvalid)
		}
		if name == "" {
			return nil
		}
		if accountName != "" && name != accountName {
			return errors.New(errEndpointAmbiguous)
		}
		accountName = name
		normalized, err := NormalizeEndpoint(u.String())
		if err != nil {
			// Generic Cognitive Services roots establish account identity, but
			// do not prove an OpenAI route. Never invent one from that root.
			if namedOpenAI {
				return errors.New(errEndpointInvalid)
			}
			return nil
		}
		priority := 40
		switch {
		case strings.EqualFold(key, "Azure OpenAI Legacy API - Latest moniker"):
			priority = 0
		case namedOpenAI:
			priority = 10
		case suffix == "openai.azure.com":
			priority = 20
		case primary:
			priority = 30
		}
		switch suffix {
		case "services.ai.azure.com":
			priority++
		case "cognitiveservices.azure.com":
			priority += 2
		}
		candidates = append(candidates, candidate{endpoint: normalized, priority: priority})
		return nil
	}
	if err := inspect(p.Endpoint, "", true); err != nil {
		return "", err
	}
	for key, raw := range p.Endpoints {
		if err := inspect(raw, key, false); err != nil {
			return "", err
		}
	}
	if len(candidates) == 0 {
		return "", errors.New(errEndpointMissing)
	}
	chosen := candidates[0]
	for _, next := range candidates[1:] {
		if next.priority < chosen.priority {
			chosen = next
		} else if next.priority == chosen.priority && next.endpoint != chosen.endpoint {
			return "", errors.New(errEndpointAmbiguous)
		}
	}
	return chosen.endpoint, nil
}
