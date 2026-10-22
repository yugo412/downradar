package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHub's endpoints. They are fields on the provider rather than constants so a
// test can point it at a server of its own.
const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubAPIURL       = "https://api.github.com"
)

// GitHub signs somebody in with their GitHub account. The address comes from the
// provider's list and only a verified one is accepted: the public profile field
// can be empty, and an unproved address would let somebody claim another account.
type GitHub struct {
	clientID     string
	clientSecret string
	httpClient   *http.Client

	authorizeURL string
	tokenURL     string
	apiURL       string
}

func NewGitHub(clientID, clientSecret string) *GitHub {
	return &GitHub{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		authorizeURL: githubAuthorizeURL,
		tokenURL:     githubTokenURL,
		apiURL:       githubAPIURL,
	}
}

func (g *GitHub) Name() string  { return "github" }
func (g *GitHub) Label() string { return "GitHub" }

// AuthorizeURL is the page where GitHub asks the person to allow the sign in.
// The state is ours and comes back untouched, which is how the callback knows
// the flow started here.
func (g *GitHub) AuthorizeURL(state, redirectURI string) string {
	query := url.Values{
		"client_id":    {g.clientID},
		"redirect_uri": {redirectURI},
		"scope":        {"read:user user:email"},
		"state":        {state},
	}
	return g.authorizeURL + "?" + query.Encode()
}

// Exchange trades the code for a token, and the token for the account behind it.
func (g *GitHub) Exchange(ctx context.Context, code, redirectURI string) (Identity, error) {
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("github: build the token request: %w", err)
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
		return Identity{}, fmt.Errorf("github: token refused: %s %s",
			token.Error, token.ErrorDescription)
	}
	if token.AccessToken == "" {
		return Identity{}, errors.New("github: the token answer carried no token")
	}

	var profile struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := g.get(ctx, token.AccessToken, "/user", &profile); err != nil {
		return Identity{}, err
	}

	var emails []githubEmail
	if err := g.get(ctx, token.AccessToken, "/user/emails", &emails); err != nil {
		return Identity{}, err
	}

	email := verifiedEmail(emails)
	if email == "" {
		return Identity{}, errors.New("github: the account has no verified address")
	}

	return Identity{
		Subject:  strconv.FormatInt(profile.ID, 10),
		Email:    email,
		Name:     profile.Name,
		Username: profile.Login,
	}, nil
}

// githubEmail is one row of the provider's address list.
type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// verifiedEmail prefers the account's primary address, and takes any other
// verified one only when there is no verified primary.
func verifiedEmail(emails []githubEmail) string {
	for _, e := range emails {
		if e.Verified && e.Primary && e.Email != "" {
			return e.Email
		}
	}
	for _, e := range emails {
		if e.Verified && e.Email != "" {
			return e.Email
		}
	}
	return ""
}

func (g *GitHub) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiURL+path, nil)
	if err != nil {
		return fmt.Errorf("github: build the %s request: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub answers 403 to a request without one.
	req.Header.Set("User-Agent", "downradar.info")

	return fetchJSON(g.httpClient, req, out)
}
