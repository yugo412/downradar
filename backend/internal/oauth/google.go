package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Google's endpoints. They are fields on the provider rather than constants so a
// test can point it at a server of its own.
const (
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleUserInfoURL  = "https://openidconnect.googleapis.com/v1/userinfo"
)

// Google signs somebody in with their Google account. The address comes from the
// userinfo endpoint, and only one Google has verified is accepted: an address
// typed into an account is no proof the mailbox is theirs.
type Google struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	authorizeURL string
	tokenURL     string
	userInfoURL  string
}

func NewGoogle(clientID, clientSecret string) *Google {
	return &Google{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		authorizeURL: googleAuthorizeURL,
		tokenURL:     googleTokenURL,
		userInfoURL:  googleUserInfoURL,
	}
}

func (g *Google) Name() string  { return "google" }
func (g *Google) Label() string { return "Google" }

// AuthorizeURL is Google's account chooser. The scopes are the three the
// userinfo endpoint needs, and the state is ours and comes back untouched.
func (g *Google) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":     {g.clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
	}
	return g.authorizeURL + "?" + query.Encode()
}

func (g *Google) Exchange(ctx context.Context, code, redirectURI string) (Identity, error) {
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("google: build the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var token struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := fetchJSON(g.httpClient, req, &token); err != nil {
		return Identity{}, err
	}
	if token.Error != "" {
		return Identity{}, fmt.Errorf("google: token refused: %s %s",
			token.Error, token.ErrorDescription)
	}
	if token.AccessToken == "" {
		return Identity{}, errors.New("google: the token answer carried no token")
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, g.userInfoURL, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("google: build the userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")

	var profile struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := fetchJSON(g.httpClient, req, &profile); err != nil {
		return Identity{}, err
	}
	if profile.Subject == "" {
		return Identity{}, errors.New("google: the account has no id")
	}
	if !profile.EmailVerified || profile.Email == "" {
		return Identity{}, errors.New("google: the account has no verified address")
	}

	return Identity{
		Subject: profile.Subject,
		Email:   profile.Email,
		Name:    profile.Name,
	}, nil
}
