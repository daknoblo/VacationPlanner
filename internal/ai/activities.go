package ai

import (
	"context"
	"encoding/json"
	"fmt"
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
// the context of a destination. baseURL, model and apiVersion may be empty.
func (c *Client) SuggestActivities(ctx context.Context, baseURL, model, apiVersion, destination, query string) ([]ActivitySuggestion, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	user := fmt.Sprintf("Destination: %s\nActivity query: %s\nSuggest matching activities.",
		strings.TrimSpace(destination), strings.TrimSpace(query))
	content, err := c.doChat(ctx, baseURL, model, apiVersion, []chatMessage{
		{Role: "system", Content: activitySystemPrompt},
		{Role: "user", Content: user},
	}, 0.6)
	if err != nil {
		return nil, err
	}
	return parseActivities(content)
}

func parseActivities(content string) ([]ActivitySuggestion, error) {
	content = stripCodeFence(strings.TrimSpace(content))
	if a := tryParseActivities(content); a != nil {
		return a, nil
	}
	if inner := extractJSON(content); inner != "" && inner != content {
		if a := tryParseActivities(inner); a != nil {
			return a, nil
		}
	}
	return nil, fmt.Errorf("ai: could not parse activities from model reply")
}

func tryParseActivities(content string) []ActivitySuggestion {
	var wrapper struct {
		Activities []ActivitySuggestion `json:"activities"`
	}
	if err := json.Unmarshal([]byte(content), &wrapper); err == nil && len(wrapper.Activities) > 0 {
		return clampActivities(wrapper.Activities)
	}
	var list []ActivitySuggestion
	if err := json.Unmarshal([]byte(content), &list); err == nil && len(list) > 0 {
		return clampActivities(list)
	}
	return nil
}

func clampActivities(a []ActivitySuggestion) []ActivitySuggestion {
	const max = 6
	if len(a) > max {
		return a[:max]
	}
	return a
}
