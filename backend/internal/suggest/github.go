package suggest

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

const defaultAPIBase = "https://api.github.com"

type Client struct {
	baseURL string
	repo    string
	token   string
	client  *http.Client
}

func NewClient(baseURL, repo, token string) *Client {
	if baseURL == "" {
		baseURL = defaultAPIBase
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		repo:    strings.Trim(repo, "/"),
		token:   token,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) CanSubmit() bool {
	return c.repo != "" && c.token != ""
}

func (c *Client) ManualURL(title, body string) string {
	if c.repo == "" {
		return ""
	}
	query := url.Values{}
	query.Set("title", title)
	query.Set("body", body)
	return fmt.Sprintf("https://github.com/%s/issues/new?%s", c.repo, query.Encode())
}

type createIssueRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type createIssueResponse struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

func (c *Client) CreateIssue(ctx context.Context, title, body string) (string, error) {
	if !c.CanSubmit() {
		return "", fmt.Errorf("suggest: github is not configured")
	}

	payload, err := json.Marshal(createIssueRequest{Title: title, Body: body})
	if err != nil {
		return "", fmt.Errorf("suggest: encode issue: %w", err)
	}

	endpoint := fmt.Sprintf("%s/repos/%s/issues", c.baseURL, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("suggest: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "downradar.info")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("suggest: create issue: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("suggest: read response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("suggest: github returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var created createIssueResponse
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return "", fmt.Errorf("suggest: decode response: %w", err)
	}
	if created.HTMLURL == "" {
		return "", fmt.Errorf("suggest: github response had no html_url")
	}
	return created.HTMLURL, nil
}
