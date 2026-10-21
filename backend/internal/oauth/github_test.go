package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// testRedirectURI is the callback the provider is told to send the browser back
// to, which the token exchange has to repeat exactly.
const testRedirectURI = "https://downradar.test/api/v1/auth/github/callback"

func TestGitHubAuthorizeURL(t *testing.T) {
	g := NewGitHub("the-client-id", "the-client-secret")

	parsed, err := url.Parse(g.AuthorizeURL("the-state", testRedirectURI))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Host != "github.com" || parsed.Path != "/login/oauth/authorize" {
		t.Errorf("authorize url = %s, want GitHub's own page", parsed)
	}

	query := parsed.Query()
	if got := query.Get("client_id"); got != "the-client-id" {
		t.Errorf("client_id = %q", got)
	}
	if got := query.Get("redirect_uri"); got != testRedirectURI {
		t.Errorf("redirect_uri = %q", got)
	}
	if got := query.Get("state"); got != "the-state" {
		t.Errorf("state = %q", got)
	}
	if got := query.Get("scope"); got != "read:user user:email" {
		t.Errorf("scope = %q", got)
	}

	// The secret belongs to the token exchange, which happens on the server.
	if query.Has("client_secret") {
		t.Error("the client secret was put in a url the browser follows")
	}
}

// githubServer is the two endpoints an exchange talks to, with the token
// exchange and the profile fixed to what a real answer looks like.
func githubServer(t *testing.T, emails []githubEmail) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			if got := r.Form.Get("code"); got != "the-code" {
				t.Errorf("code = %q", got)
			}
			if got := r.Form.Get("client_id"); got != "the-client-id" {
				t.Errorf("client_id = %q", got)
			}
			if got := r.Form.Get("client_secret"); got != "the-client-secret" {
				t.Errorf("client_secret = %q", got)
			}
			if got := r.Form.Get("redirect_uri"); got != testRedirectURI {
				t.Errorf("redirect_uri = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "the-token"})

		case "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer the-token" {
				t.Errorf("Authorization = %q", got)
			}
			if r.Header.Get("User-Agent") == "" {
				t.Error("no User-Agent, which GitHub refuses")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 42, "login": "octo", "name": "Octo Cat",
			})

		case "/user/emails":
			_ = json.NewEncoder(w).Encode(emails)

		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func newTestGitHub(server *httptest.Server) *GitHub {
	g := NewGitHub("the-client-id", "the-client-secret")
	g.tokenURL = server.URL + "/login/oauth/access_token"
	g.apiURL = server.URL
	return g
}

func TestGitHubExchangeReturnsTheVerifiedPrimaryAddress(t *testing.T) {
	server := githubServer(t, []githubEmail{
		{Email: "other@example.invalid", Verified: true},
		{Email: "primary@example.invalid", Primary: true, Verified: true},
	})
	defer server.Close()

	identity, err := newTestGitHub(server).Exchange(context.Background(), "the-code", testRedirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if identity.Subject != "42" {
		t.Errorf("subject = %q, want the provider's own id as a string", identity.Subject)
	}
	if identity.Email != "primary@example.invalid" {
		t.Errorf("email = %q, want the verified primary", identity.Email)
	}
	if identity.Name != "Octo Cat" || identity.Username != "octo" {
		t.Errorf("name = %q, username = %q", identity.Name, identity.Username)
	}
}

func TestGitHubExchangeUsesAnotherVerifiedAddressWhenThePrimaryIsNot(t *testing.T) {
	server := githubServer(t, []githubEmail{
		{Email: "unproved@example.invalid", Primary: true},
		{Email: "proved@example.invalid", Verified: true},
	})
	defer server.Close()

	identity, err := newTestGitHub(server).Exchange(context.Background(), "the-code", testRedirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if identity.Email != "proved@example.invalid" {
		t.Errorf("email = %q, want the verified one", identity.Email)
	}
}

func TestGitHubExchangeRefusesAnUnprovedAddress(t *testing.T) {
	server := githubServer(t, []githubEmail{
		{Email: "unproved@example.invalid", Primary: true},
	})
	defer server.Close()

	if _, err := newTestGitHub(server).Exchange(context.Background(), "the-code", testRedirectURI); err == nil {
		t.Fatal("an account with no verified address was accepted")
	}
}

func TestGitHubExchangeReportsARefusedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "bad_verification_code", "error_description": "The code passed is incorrect or expired.",
		})
	}))
	defer server.Close()

	_, err := newTestGitHub(server).Exchange(context.Background(), "the-code", testRedirectURI)
	if err == nil {
		t.Fatal("a refused code was accepted")
	}
}
