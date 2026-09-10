package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// DoChat performs exactly one non-streaming text chat request. It rejects
// stale/tampered targets before obtaining a token and never retries generation.
func (c *Client) DoChat(ctx context.Context, target Target, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, errors.New("foundry chat was canceled before generation")
	}
	select {
	case c.chatGate <- struct{}{}:
		defer func() { <-c.chatGate }()
	default:
		return nil, errors.New("foundry chat is busy; four generation requests are already in progress")
	}
	expected, err := c.ResolveChat(ctx, target.Deployment)
	if err != nil {
		return nil, err
	}
	if target != expected {
		return nil, errors.New("foundry chat target does not match the configured main resource and deployment")
	}
	if err := validateChatPayload(target, payload); err != nil {
		return nil, err
	}
	endpoint := target.Endpoint + "/chat/completions"
	if target.APIVersion != "" {
		endpoint = target.Endpoint + "/openai/deployments/" + url.PathEscape(target.Deployment) +
			"/chat/completions?api-version=" + url.QueryEscape(target.APIVersion)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("foundry chat request could not be constructed")
	}
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{inferenceScope}})
	if err != nil || token.Token == "" {
		return nil, errors.New("foundry chat authentication failed; check service principal configuration")
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.inference.Do(req)
	if err != nil {
		return nil, errors.New("foundry chat connection failed or timed out; generation was not retried")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChatBytes+1))
	if len(body) > maxChatBytes {
		return nil, errors.New("foundry chat response exceeded the size limit")
	}
	if err != nil {
		return nil, errors.New("foundry chat response could not be read; generation was not retried")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, chatHTTPError(resp.StatusCode)
	}
	if !json.Valid(body) {
		return nil, errors.New("foundry chat returned an invalid JSON response")
	}
	return body, nil
}

func chatHTTPError(status int) error {
	reason := "provider request failed"
	switch status {
	case http.StatusUnauthorized:
		reason = "authentication was rejected"
	case http.StatusForbidden:
		reason = "inference access was denied"
	case http.StatusNotFound:
		reason = "deployment or inference route was not found"
	case http.StatusTooManyRequests:
		reason = "quota or rate limit was reached"
	case http.StatusBadRequest:
		reason = "provider rejected the request parameters"
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusSeeOther:
		reason = "redirect was blocked"
	}
	return fmt.Errorf("foundry chat: HTTP %d (%s); generation was not retried", status, reason)
}

func validateChatPayload(target Target, payload []byte) error {
	invalid := errors.New("foundry chat requires a bounded text-only request for the selected deployment; tools, images and streaming are unsupported")
	if len(payload) == 0 || len(payload) > maxChatBytes {
		return invalid
	}
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Temperature         *float64 `json:"temperature,omitempty"`
		MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
		MaxTokens           int      `json:"max_tokens,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || !json.Valid(payload) ||
		req.Model != target.Deployment || len(req.Messages) == 0 || len(req.Messages) > 100 ||
		(req.Temperature != nil && (!target.SupportsTemperature || *req.Temperature < 0 || *req.Temperature > 2)) ||
		req.MaxTokens < 0 || req.MaxCompletionTokens < 0 ||
		(req.MaxTokens != 0 && req.MaxCompletionTokens != 0) {
		return invalid
	}
	for _, message := range req.Messages {
		if (message.Role != "system" && message.Role != "user" && message.Role != "assistant" && message.Role != "developer") ||
			message.Content == "" {
			return invalid
		}
	}
	return nil
}
