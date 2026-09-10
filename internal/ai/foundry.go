package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/daknoblo/vacationplanner/internal/foundry"
)

// FoundryBackend resolves and authorizes each call against the configured account.
type FoundryBackend interface {
	ResolveChat(context.Context, string) (foundry.Target, error)
	DoChat(context.Context, foundry.Target, []byte) ([]byte, error)
}

// New builds the recommendation client; a nil backend disables AI.
func New(backend FoundryBackend) *Client {
	return &Client{foundry: backend}
}

func (c *Client) foundryChat(ctx context.Context, deployment string, messages []chatMessage, temperature float64, maxTokens int) (string, error) {
	target, err := c.foundry.ResolveChat(ctx, deployment)
	if err != nil {
		return "", err
	}
	return c.foundryChatTarget(ctx, target, messages, temperature, maxTokens)
}

func (c *Client) foundryChatTarget(ctx context.Context, target foundry.Target, messages []chatMessage, temperature float64, maxTokens int) (string, error) {
	request := struct {
		Model               string        `json:"model"`
		Messages            []chatMessage `json:"messages"`
		Temperature         *float64      `json:"temperature,omitempty"`
		MaxCompletionTokens int           `json:"max_completion_tokens"`
	}{
		Model: target.Deployment, Messages: messages, MaxCompletionTokens: maxTokens,
	}
	if target.SupportsTemperature {
		request.Temperature = &temperature
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("ai: encoding chat request: %w", err)
	}
	body, err := c.foundry.DoChat(ctx, target, payload)
	if err != nil {
		return "", err
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("ai: chat returned invalid JSON")
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("ai: chat provider reported an error")
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("ai: chat returned no text")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Probe performs a deliberately small, billable text request, never discovery or images.
func (c *Client) Probe(ctx context.Context, target foundry.Target) error {
	if c.foundry == nil {
		return fmt.Errorf("ai: Foundry mode is not configured")
	}
	_, err := c.foundryChatTarget(ctx, target, []chatMessage{{Role: "user", Content: "Reply with OK."}}, 0, 256)
	return err
}
