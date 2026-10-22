package slack

import (
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

// Slack's own endpoints. They are fields on the app rather than constants so a
// test can point the exchange at a server of its own.
const (
	slackAuthorizeURL = "https://slack.com/oauth/v2/authorize"
	slackAccessURL    = "https://slack.com/api/oauth.v2.access"
)

// App is the site's own Slack app, which is what the one click install uses.
// Only the client id and secret are needed: the install asks for the
// incoming-webhook scope, so the answer carries the URL of the channel the
// person picked and the app never has to read the workspace's messages.
type App struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	authorizeURL string
	accessURL    string
}

func NewApp(clientID, clientSecret string) *App {
	return &App{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		authorizeURL: slackAuthorizeURL,
		accessURL:    slackAccessURL,
	}
}

// AuthorizeURL is the page where Slack asks which workspace and channel to post
// to. The state is ours and comes back untouched, which is how the callback
// knows the answer belongs to a flow this browser started. Slack matches the
// redirect address exactly, so it is a fixed path of its own rather than one
// that names the alert.
func (a *App) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":    {a.clientID},
		"scope":        {"incoming-webhook"},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return a.authorizeURL + "?" + query.Encode()
}

// Webhook is what an install leaves behind: where to post, the channel the
// person picked, and the workspace it belongs to.
type Webhook struct {
	URL     string
	Channel string
	Team    string
}

// Exchange trades the code Slack sent back for the channel's webhook. Slack
// answers a refusal with a 200 and ok false, so the error field is what decides
// whether the answer is usable.
func (a *App) Exchange(ctx context.Context, code, redirectURI string) (Webhook, error) {
	form := url.Values{
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.accessURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Webhook{}, fmt.Errorf("slack: build the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := a.httpClient.Do(req)
	if err != nil {
		return Webhook{}, fmt.Errorf("slack: exchange the code: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Webhook{}, fmt.Errorf("slack: read the answer: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Webhook{}, fmt.Errorf("slack: exchange answered %d: %s",
			res.StatusCode, strings.TrimSpace(string(body)))
	}

	var answer struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Team  struct {
			Name string `json:"name"`
		} `json:"team"`
		IncomingWebhook struct {
			URL     string `json:"url"`
			Channel string `json:"channel"`
		} `json:"incoming_webhook"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return Webhook{}, fmt.Errorf("slack: decode the answer: %w", err)
	}
	if !answer.OK {
		if answer.Error == "" {
			answer.Error = "the answer was not ok"
		}
		return Webhook{}, fmt.Errorf("slack: exchange refused: %s", answer.Error)
	}
	if answer.IncomingWebhook.URL == "" {
		return Webhook{}, errors.New("slack: the answer carried no incoming webhook")
	}

	return Webhook{
		URL:     answer.IncomingWebhook.URL,
		Channel: answer.IncomingWebhook.Channel,
		Team:    answer.Team.Name,
	}, nil
}
