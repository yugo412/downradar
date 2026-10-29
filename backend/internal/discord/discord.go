// Package discord installs the site's Discord app into a server channel through
// OAuth2. The webhook.incoming scope is the Discord equivalent of Slack's
// incoming webhook: the answer carries a webhook for the channel the person
// picked, so no bot token is needed to post alerts.
package discord

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

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
)

// App is the site's own Discord application. Only the client id and secret are
// needed: the install asks for webhook.incoming, which lets Discord make the
// channel's incoming webhook.
type App struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	authorizeURL string
	tokenURL     string
}

func NewApp(clientID, clientSecret string) *App {
	return &App{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		authorizeURL: discordAuthorizeURL,
		tokenURL:     discordTokenURL,
	}
}

// AuthorizeURL is the page where Discord asks which server and channel to post
// to. The state comes back untouched, and Discord matches the redirect address
// exactly.
func (a *App) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"response_type": {"code"},
		"client_id":     {a.clientID},
		"scope":         {"webhook.incoming"},
		"state":         {state},
		"redirect_uri":  {redirectURI},
	}
	return a.authorizeURL + "?" + query.Encode()
}

// Webhook is what an install leaves behind: where to post, and the channel and
// server it belongs to. Discord returns ids rather than names for the channel.
type Webhook struct {
	URL     string
	Name    string
	Channel string
	Guild   string
}

// Exchange trades the code Discord sent back for the channel's incoming webhook.
// Discord answers a refusal with a non-2xx status or an error field, so both are
// checked before the webhook is used.
func (a *App) Exchange(ctx context.Context, code, redirectURI string) (Webhook, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Webhook{}, fmt.Errorf("discord: build the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := a.httpClient.Do(req)
	if err != nil {
		return Webhook{}, fmt.Errorf("discord: exchange the code: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Webhook{}, fmt.Errorf("discord: read the answer: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Webhook{}, fmt.Errorf("discord: exchange answered %d: %s",
			res.StatusCode, strings.TrimSpace(string(body)))
	}

	var answer struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		Webhook          struct {
			URL       string `json:"url"`
			Name      string `json:"name"`
			ChannelID string `json:"channel_id"`
			GuildID   string `json:"guild_id"`
		} `json:"webhook"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return Webhook{}, fmt.Errorf("discord: decode the answer: %w", err)
	}
	if answer.Error != "" {
		if answer.ErrorDescription != "" {
			return Webhook{}, fmt.Errorf("discord: exchange refused: %s: %s",
				answer.Error, answer.ErrorDescription)
		}
		return Webhook{}, fmt.Errorf("discord: exchange refused: %s", answer.Error)
	}
	if answer.Webhook.URL == "" {
		return Webhook{}, errors.New("discord: the answer carried no webhook")
	}

	return Webhook{
		URL:     answer.Webhook.URL,
		Name:    answer.Webhook.Name,
		Channel: answer.Webhook.ChannelID,
		Guild:   answer.Webhook.GuildID,
	}, nil
}
