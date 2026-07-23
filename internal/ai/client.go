// Package ai provides a minimal client for OpenAI-compatible chat completion
// endpoints (OpenAI, Azure OpenAI, Ollama, LocalAI, vLLM, ...).
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default endpoint settings used when no override is configured.
const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-4o-mini"
)

// Client talks to an OpenAI-compatible /chat/completions endpoint.
type Client struct {
	http   *http.Client
	apiKey string
}

// New builds a client using the given API key. When the key is empty the client
// is disabled and Recommend returns ErrDisabled. The endpoint URL and model are
// passed per call, since they are configured at runtime (not baked into the client).
func New(apiKey string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 60 * time.Second},
		apiKey: apiKey,
	}
}

// Enabled reports whether an API key is configured.
func (c *Client) Enabled() bool { return c.apiKey != "" }

// ErrDisabled is returned when AI features are used without an API key.
var ErrDisabled = fmt.Errorf("ai: no API key configured")

// Suggestion is a single recommended point of interest.
type Suggestion struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

// RecommendInput carries the trip context used to build the prompt.
type RecommendInput struct {
	Destination string
	StartDate   string
	EndDate     string
	Interests   string
	RadiusKm    int
	Existing    []string
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

const systemPrompt = `You are a concise travel assistant. ` +
	`Given a destination and trip context, suggest real, notable points of interest that are located AT or NEAR that destination — ` +
	`in the SAME city/region and the SAME country. Never suggest places in a different country or a far-away region. ` +
	`Respond with STRICT JSON only, no markdown, in this exact shape: ` +
	`{"suggestions":[{"name":"...","category":"...","description":"...","reason":"..."}]}. ` +
	`Provide between 4 and 6 suggestions. Keep description and reason to one short sentence each.`

// Recommend asks the model for points of interest for the given trip. baseURL,
// model and apiVersion may be empty, in which case the package defaults / a
// plain (non-Azure) request are used.
func (c *Client) Recommend(ctx context.Context, baseURL, model, apiVersion string, in RecommendInput) ([]Suggestion, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	content, err := c.doChat(ctx, baseURL, model, apiVersion, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: buildUserPrompt(in)},
	}, 0.7)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(content)
}

// buildEndpoint constructs the chat-completions URL for the request. When
// apiVersion is set it targets Azure OpenAI, where the deployment name (passed
// as the model) is part of the path — unless the base URL already points at a
// deployment — and an ?api-version query parameter is required. Otherwise it
// uses the standard OpenAI-compatible "{baseURL}/chat/completions" path.
func buildEndpoint(baseURL, model, apiVersion string) string {
	apiVer := strings.TrimSpace(apiVersion)
	var endpoint string
	if apiVer != "" && !strings.Contains(baseURL, "/deployments/") {
		endpoint = baseURL + "/openai/deployments/" + url.PathEscape(model) + "/chat/completions"
	} else {
		endpoint = baseURL + "/chat/completions"
	}
	if apiVer != "" {
		endpoint += "?api-version=" + url.QueryEscape(apiVer)
	}
	return endpoint
}

// doChat performs a chat-completion request and returns the assistant message
// content. When apiVersion is set it targets an Azure OpenAI-style endpoint
// (?api-version=... plus the api-key header) alongside the Bearer token.
func (c *Client) doChat(ctx context.Context, baseURL, model, apiVersion string, messages []chatMessage, temperature float64) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}

	payload, err := json.Marshal(chatRequest{Model: model, Temperature: temperature, Messages: messages})
	if err != nil {
		return "", fmt.Errorf("ai: encoding request: %w", err)
	}

	endpoint := buildEndpoint(baseURL, model, apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ai: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if strings.TrimSpace(apiVersion) != "" {
		req.Header.Set("api-key", c.apiKey) // Azure OpenAI style
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: calling endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("ai: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Surface the endpoint and the endpoint's own error message/body so a
		// misconfiguration (wrong base URL, model or deployment) is diagnosable
		// from the logs. The API key travels in headers, never in the URL.
		detail := bodySnippet(body)
		var parsed chatResponse
		if json.Unmarshal(body, &parsed) == nil && parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			detail = oneLine(parsed.Error.Message)
		}
		return "", fmt.Errorf("ai: %s POST %s returned status %d: %s",
			model, endpoint, resp.StatusCode, detail)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("ai: decoding response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("ai: endpoint error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("ai: endpoint returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// oneLine collapses whitespace so a value is safe to embed in a single-line log
// or error message (also mitigates log injection).
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(strings.TrimSpace(s))
}

// bodySnippet returns a compact, length-capped view of a response body.
func bodySnippet(b []byte) string {
	s := oneLine(string(b))
	if s == "" {
		return "(empty body)"
	}
	const maxLen = 300
	if r := []rune(s); len(r) > maxLen {
		s = string(r[:maxLen]) + "…"
	}
	return s
}

func buildUserPrompt(in RecommendInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Destination: %s\n", in.Destination)
	fmt.Fprintf(&b, "Only suggest places within about %d km of %s, in the same country and region.\n",
		radiusOrDefault(in.RadiusKm), in.Destination)
	if in.StartDate != "" || in.EndDate != "" {
		fmt.Fprintf(&b, "Travel dates: %s to %s\n", in.StartDate, in.EndDate)
	}
	if strings.TrimSpace(in.Interests) != "" {
		fmt.Fprintf(&b, "Focus on these interests: %s\n", in.Interests)
	} else {
		b.WriteString("No specific interests were given: suggest the most notable sights and points of interest for this area.\n")
	}
	if len(in.Existing) > 0 {
		fmt.Fprintf(&b, "Already planned (do not repeat): %s\n", strings.Join(in.Existing, ", "))
	}
	b.WriteString("Suggest points of interest.")
	return b.String()
}

// radiusOrDefault clamps the search radius (km) to a sensible range.
func radiusOrDefault(km int) int {
	switch {
	case km <= 0:
		return 25
	case km > 500:
		return 500
	default:
		return km
	}
}

// parseSuggestions tolerantly extracts suggestions from a model reply that may
// be wrapped in markdown fences or returned as a bare array or embedded in prose.
func parseSuggestions(content string) ([]Suggestion, error) {
	content = stripCodeFence(strings.TrimSpace(content))
	if s := tryParseSuggestions(content); s != nil {
		return s, nil
	}
	if inner := extractJSON(content); inner != "" && inner != content {
		if s := tryParseSuggestions(inner); s != nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("ai: could not parse suggestions from model reply")
}

func tryParseSuggestions(content string) []Suggestion {
	var wrapper struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil && len(wrapper.Suggestions) > 0 {
		return clamp(wrapper.Suggestions)
	}
	var list []Suggestion
	if err := json.Unmarshal([]byte(content), &list); err == nil && len(list) > 0 {
		return clamp(list)
	}
	return nil
}

func clamp(s []Suggestion) []Suggestion {
	const max = 8
	if len(s) > max {
		return s[:max]
	}
	return s
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// extractJSON returns the substring spanning the first JSON object or array in s
// (from the first '{' or '[' to its matching close), or "" if none is found. It
// is a best-effort recovery for models that wrap the JSON in explanatory prose.
func extractJSON(s string) string {
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return ""
	}
	openCh := s[start]
	closeCh := byte('}')
	if openCh == '[' {
		closeCh = ']'
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case openCh:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
