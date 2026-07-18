package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DefaultOpenAIEndpoint is the OpenAI Moderation API base URL.
const DefaultOpenAIEndpoint = "https://api.openai.com/v1/moderations"

// OpenAIModerationProvider implements ModerationProvider via the OpenAI
// Moderation API.
type OpenAIModerationProvider struct {
	Endpoint string
	APIKey   string
	client   *http.Client
}

// NewOpenAIProvider builds a provider with sensible defaults. endpoint and
// apiKey may be empty strings to use defaults or defer to env.
func NewOpenAIProvider(endpoint, apiKey string, timeout time.Duration) *OpenAIModerationProvider {
	if endpoint == "" {
		endpoint = DefaultOpenAIEndpoint
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &OpenAIModerationProvider{
		Endpoint: endpoint,
		APIKey:   apiKey,
		client:   &http.Client{Timeout: timeout},
	}
}

// Check sends the content to the OpenAI moderation endpoint. categories is
// currently unused by the OpenAI API (it returns results for all built-in
// categories); it is accepted for interface compatibility with future providers.
func (p *OpenAIModerationProvider) Check(ctx context.Context, content string, categories []string) (bool, error) {
	body := openAIRequest{
		Input: content,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false, fmt.Errorf("moderation: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("moderation: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("moderation: call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("moderation: unexpected status %d", resp.StatusCode)
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("moderation: decode response: %w", err)
	}

	for _, r := range result.Results {
		if r.Flagged {
			return true, nil
		}
	}
	return false, nil
}

type openAIRequest struct {
	Input string `json:"input"`
}

type openAIResponse struct {
	Results []openAIResult `json:"results"`
}

type openAIResult struct {
	Flagged    bool               `json:"flagged"`
	Categories map[string]bool    `json:"categories"`
	Scores     map[string]float64 `json:"category_scores"`
}
