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
	`Given a destination and trip context, suggest points of interest. ` +
	`Respond with STRICT JSON only, no markdown, in this exact shape: ` +
	`{"suggestions":[{"name":"...","category":"...","description":"...","reason":"..."}]}. ` +
	`Provide between 3 and 6 suggestions. Keep description and reason to one short sentence each.`

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

	endpoint := baseURL + "/chat/completions"
	if v := strings.TrimSpace(apiVersion); v != "" {
		endpoint += "?api-version=" + url.QueryEscape(v)
	}
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
		return "", fmt.Errorf("ai: endpoint returned status %d", resp.StatusCode)
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

func buildUserPrompt(in RecommendInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Destination: %s\n", in.Destination)
	if in.StartDate != "" || in.EndDate != "" {
		fmt.Fprintf(&b, "Travel dates: %s to %s\n", in.StartDate, in.EndDate)
	}
	if strings.TrimSpace(in.Interests) != "" {
		fmt.Fprintf(&b, "Interests: %s\n", in.Interests)
	}
	if len(in.Existing) > 0 {
		fmt.Fprintf(&b, "Already planned (do not repeat): %s\n", strings.Join(in.Existing, ", "))
	}
	b.WriteString("Suggest additional points of interest.")
	return b.String()
}

// parseSuggestions tolerantly extracts suggestions from a model reply that may
// be wrapped in markdown fences or returned as a bare array.
func parseSuggestions(content string) ([]Suggestion, error) {
	content = stripCodeFence(strings.TrimSpace(content))

	var wrapper struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil && len(wrapper.Suggestions) > 0 {
		return clamp(wrapper.Suggestions), nil
	}

	var list []Suggestion
	if err := json.Unmarshal([]byte(content), &list); err == nil && len(list) > 0 {
		return clamp(list), nil
	}

	return nil, fmt.Errorf("ai: could not parse suggestions from model reply")
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
