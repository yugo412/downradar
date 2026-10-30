package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// OpenCode routes by this header and expects a stable value per conversation.
	opencodeSession = "downradar-summary"
)

// OpenAI is the writer that turns the state prompt into one short paragraph. It
// speaks the chat completions shape, which keeps the base URL configurable for a
// local or compatible endpoint.
type OpenAI struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

func NewOpenAI(apiKey, baseURL, model string, timeout time.Duration) *OpenAI {
	return &OpenAI{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimSuffix(strings.TrimSpace(baseURL), "/"),
		model:   strings.TrimSpace(model),
		client:  &http.Client{Timeout: timeout},
	}
}

func (o *OpenAI) Write(ctx context.Context, system, user string) (string, error) {
	body, err := json.Marshal(struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model: o.model,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", fmt.Errorf("openai: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "downradar.info/0.1 (+https://downradar.info)")
	req.Header.Set("x-opencode-session", opencodeSession)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var answer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &answer); err != nil {
		return "", fmt.Errorf("openai: decode response: %w", err)
	}
	if answer.Error != nil {
		return "", fmt.Errorf("openai: %s", answer.Error.Message)
	}
	if len(answer.Choices) == 0 {
		return "", fmt.Errorf("openai: response has no choices")
	}
	return answer.Choices[0].Message.Content, nil
}
