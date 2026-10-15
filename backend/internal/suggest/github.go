package suggest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.github.com"

var errLabelsRejected = errors.New("suggest: github rejected the labels")

type Client struct {
	baseURL string
	repo    string
	token   string
	labels  []string
	client  *http.Client
}

func NewClient(baseURL, repo, token string, labels []string) *Client {
	if baseURL == "" {
		baseURL = defaultAPIBase
	}
	cleaned := make([]string, 0, len(labels))
	for _, label := range labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		repo:    strings.Trim(repo, "/"),
		token:   token,
		labels:  cleaned,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) CanSubmit() bool {
	return c.repo != "" && c.token != ""
}

func (c *Client) Labels() []string {
	return append([]string(nil), c.labels...)
}

func (c *Client) ManualURL(title, body string) string {
	if c.repo == "" {
		return ""
	}
	query := url.Values{}
	query.Set("title", title)
	query.Set("body", body)
	if len(c.labels) > 0 {
		query.Set("labels", strings.Join(c.labels, ","))
	}
	return fmt.Sprintf("https://github.com/%s/issues/new?%s", c.repo, query.Encode())
}

type createIssueRequest struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

type createIssueResponse struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

func (c *Client) CreateIssue(ctx context.Context, title, body string) (string, error) {
	if !c.CanSubmit() {
		return "", fmt.Errorf("suggest: github is not configured")
	}

	issueURL, err := c.postIssue(ctx, title, body, c.labels)
	if err == nil {
		return issueURL, nil
	}
	if !errors.Is(err, errLabelsRejected) || len(c.labels) == 0 {
		return "", err
	}

	issueURL, retryErr := c.postIssue(ctx, title, body, nil)
	if retryErr != nil {
		return "", fmt.Errorf("%w (retrying without labels also failed: %v)", err, retryErr)
	}
	return issueURL, nil
}

func (c *Client) postIssue(ctx context.Context, title, body string, labels []string) (string, error) {
	payload, err := json.Marshal(createIssueRequest{Title: title, Body: body, Labels: labels})
	if err != nil {
		return "", fmt.Errorf("suggest: encode issue: %w", err)
	}

	status, responseBody, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/repos/%s/issues", c.baseURL, c.repo), payload)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated {
		return "", githubError(status, responseBody)
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

type createLabelRequest struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func (c *Client) EnsureLabels(ctx context.Context) error {
	if !c.CanSubmit() {
		return nil
	}

	var failures []string
	for _, label := range c.labels {
		payload, err := json.Marshal(createLabelRequest{
			Name:        label,
			Color:       "c5def5",
			Description: "Suggested from the website",
		})
		if err != nil {
			return fmt.Errorf("suggest: encode label %q: %w", label, err)
		}

		status, body, err := c.do(ctx, http.MethodPost,
			fmt.Sprintf("%s/repos/%s/labels", c.baseURL, c.repo), payload)
		if err != nil {
			return err
		}
		switch status {
		case http.StatusCreated:
		case http.StatusUnprocessableEntity:
		default:
			failures = append(failures, fmt.Sprintf("%s: %s", label, githubError(status, body)))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("suggest: could not prepare labels: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("suggest: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "downradar.info")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("suggest: request %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("suggest: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func githubError(status int, body []byte) error {
	base := fmt.Errorf("suggest: github returned HTTP %d: %s", status, strings.TrimSpace(string(body)))

	if status != http.StatusUnprocessableEntity {
		return base
	}

	var failure struct {
		Errors []struct {
			Field string `json:"field"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		return base
	}
	for _, item := range failure.Errors {
		if item.Field == "labels" {
			return fmt.Errorf("%w: %w", errLabelsRejected, base)
		}
	}
	return base
}
