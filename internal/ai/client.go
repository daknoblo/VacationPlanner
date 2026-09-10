// Package ai generates travel recommendations through resource-bound Foundry chat.
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Client uses a discovered deployment without accepting endpoints or credentials.
type Client struct {
	foundry FoundryBackend
}

// Enabled reports whether a Foundry connection is configured.
func (c *Client) Enabled() bool { return c.foundry != nil }

// ErrDisabled is returned when AI features are used without a Foundry connection.
var ErrDisabled = fmt.Errorf("ai: Foundry identity is not configured")

// Suggestion is a single recommended point of interest.
type Suggestion struct {
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Reason      string  `json:"reason"`
	Website     string  `json:"website"`
	Rating      float64 `json:"rating"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

// RecommendInput carries the trip context used to build the prompt.
type RecommendInput struct {
	Destination string
	StartDate   string
	EndDate     string
	Interests   string
	RadiusKm    int
	Count       int
	Existing    []string
	// Origin is the search center chosen by the user (a label such as a place
	// name). When HasOrigin is set, OriginLat/OriginLng give its exact
	// coordinates and the radius is measured from that point instead of the
	// trip destination.
	Origin    string
	OriginLat float64
	OriginLng float64
	HasOrigin bool
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	`Given a search center and trip context, suggest real, notable points of interest that are located AT or NEAR that search center — ` +
	`within the given radius, in the SAME country. Never suggest places in a different country or far outside the radius. ` +
	`When coordinates are given, treat them as the authoritative center. ` +
	`Respond with STRICT JSON only, no markdown, in this exact shape: ` +
	`{"suggestions":[{"name":"...","category":"...","description":"...","reason":"...","website":"...","rating":4.5,"latitude":0.0,"longitude":0.0}]}. ` +
	`Set "website" to the official website URL (starting with https://) when you are confident it is correct, otherwise use an empty string; never invent a URL. ` +
	`Set "rating" to the place's typical visitor rating out of 5 with one decimal (e.g. 4.5) when it is well-known, otherwise 0. ` +
	`Set "latitude" and "longitude" to the place's approximate WGS84 coordinates in decimal degrees when known, otherwise 0. ` +
	`Keep description and reason to one short sentence each.`

// Recommend asks the selected account deployment for points of interest.
func (c *Client) Recommend(ctx context.Context, deployment string, in RecommendInput) ([]Suggestion, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	content, err := c.foundryChat(ctx, deployment, []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: buildUserPrompt(in)},
	}, 0.7, 8192)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(content)
}

func buildUserPrompt(in RecommendInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trip destination: %s\n", in.Destination)
	center := strings.TrimSpace(in.Origin)
	if center == "" {
		center = in.Destination
	}
	if in.HasOrigin {
		fmt.Fprintf(&b, "Search center: %s (latitude %.5f, longitude %.5f). Use these coordinates as the authoritative center.\n",
			center, in.OriginLat, in.OriginLng)
		fmt.Fprintf(&b, "Only suggest places within about %d km of that search center, in the same country.\n",
			radiusOrDefault(in.RadiusKm))
	} else {
		fmt.Fprintf(&b, "Only suggest places within about %d km of %s, in the same country and region.\n",
			radiusOrDefault(in.RadiusKm), center)
	}
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
	fmt.Fprintf(&b, "Suggest exactly %d points of interest.", countOrDefault(in.Count))
	return b.String()
}

// countOrDefault clamps the requested number of suggestions to a sensible range.
func countOrDefault(n int) int {
	switch {
	case n <= 0:
		return 5
	case n > 25:
		return 25
	default:
		return n
	}
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
	const max = 25
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
