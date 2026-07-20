package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ActivitySuggestion is a single AI-suggested activity for a destination.
type ActivitySuggestion struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

const activitySystemPrompt = `You are a concise travel assistant. Given a destination and a partial ` +
	`activity query, suggest matching things to do (attractions, tours, experiences, food). ` +
	`Respond with STRICT JSON only, no markdown, in this exact shape: ` +
	`{"activities":[{"name":"...","category":"...","description":"..."}]}. ` +
	`Provide between 3 and 6 suggestions relevant to the destination and query. ` +
	`Keep each description to one short, informative sentence.`

// SuggestActivities asks the model for activities matching a partial query in
// the context of a destination. baseURL and model may be empty (defaults used).
func (c *Client) SuggestActivities(ctx context.Context, baseURL, model, destination, query string) ([]ActivitySuggestion, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}

	user := fmt.Sprintf("Destination: %s\nActivity query: %s\nSuggest matching activities.",
		strings.TrimSpace(destination), strings.TrimSpace(query))
	reqBody := chatRequest{
		Model:       model,
		Temperature: 0.6,
		Messages: []chatMessage{
			{Role: "system", Content: activitySystemPrompt},
			{Role: "user", Content: user},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ai: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ai: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ai: calling endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ai: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ai: endpoint returned status %d", resp.StatusCode)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ai: decoding response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("ai: endpoint error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("ai: endpoint returned no choices")
	}
	return parseActivities(parsed.Choices[0].Message.Content)
}

func parseActivities(content string) ([]ActivitySuggestion, error) {
	content = stripCodeFence(strings.TrimSpace(content))

	var wrapper struct {
		Activities []ActivitySuggestion `json:"activities"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil && len(wrapper.Activities) > 0 {
		return clampActivities(wrapper.Activities), nil
	}
	var list []ActivitySuggestion
	if err := json.Unmarshal([]byte(content), &list); err == nil && len(list) > 0 {
		return clampActivities(list), nil
	}
	return nil, fmt.Errorf("ai: could not parse activities from model reply")
}

func clampActivities(a []ActivitySuggestion) []ActivitySuggestion {
	const max = 6
	if len(a) > max {
		return a[:max]
	}
	return a
}
