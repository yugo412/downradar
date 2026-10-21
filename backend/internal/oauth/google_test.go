package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The callback the provider is told to send the browser back to, which the token
// exchange has to repeat exactly.
const testGoogleRedirectURI = "https://downradar.test/api/v1/auth/google/callback"

func TestGoogleAuthorizeURL(t *testing.T) {
	g := NewGoogle("the-google-id", "the-google-secret")

	parsed, err := url.Parse(g.AuthorizeURL("the-state", testGoogleRedirectURI))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Host != "accounts.google.com" || parsed.Path != "/o/oauth2/v2/auth" {
		t.Errorf("authorize url = %s, want Google's own page", parsed)
	}

	query := parsed.Query()
	for key, want := range map[string]string{
		"client_id":     "the-google-id",
		"redirect_uri":  testGoogleRedirectURI,
		"response_type": "code",
		"scope":         "openid email profile",
		"state":         "the-state",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// The secret belongs to the token exchange, which happens on the server.
	if query.Has("client_secret") {
		t.Error("the client secret was put in a url the browser follows")
	}
}

// googleServer is the two endpoints an exchange talks to. The address it reports
// is verified unless the test says otherwise.
func googleServer(t *testing.T, emailVerified bool) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/token":
			if r.Method != http.MethodPost {
				t.Errorf("token method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			for key, want := range map[string]string{
				"grant_type":    "authorization_code",
				"code":          "the-code",
				"client_id":     "the-google-id",
				"client_secret": "the-google-secret",
				"redirect_uri":  testGoogleRedirectURI,
			} {
				if got := r.Form.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "the-token"})

		case "/v1/userinfo":
			if got := r.Header.Get("Authorization"); got != "Bearer the-token" {
				t.Errorf("Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub":            "1234567890",
				"email":          "person@example.invalid",
				"email_verified": emailVerified,
				"name":           "Google Person",
			})

		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func newTestGoogle(server *httptest.Server) *Google {
	g := NewGoogle("the-google-id", "the-google-secret")
	g.tokenURL = server.URL + "/token"
	g.userInfoURL = server.URL + "/v1/userinfo"
	return g
}

func TestGoogleExchangeReturnsTheVerifiedAddress(t *testing.T) {
	server := googleServer(t, true)
	defer server.Close()

	identity, err := newTestGoogle(server).Exchange(context.Background(), "the-code", testGoogleRedirectURI)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if identity.Subject != "1234567890" {
		t.Errorf("subject = %q, want Google's own id", identity.Subject)
	}
	if identity.Email != "person@example.invalid" {
		t.Errorf("email = %q", identity.Email)
	}
	if identity.Name != "Google Person" {
		t.Errorf("name = %q", identity.Name)
	}
}

func TestGoogleExchangeRefusesAnUnverifiedAddress(t *testing.T) {
	server := googleServer(t, false)
	defer server.Close()

	if _, err := newTestGoogle(server).Exchange(context.Background(), "the-code", testGoogleRedirectURI); err == nil {
		t.Fatal("an unverified address was accepted")
	}
}

func TestGoogleExchangeReportsARefusedCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid_grant", "error_description": "Bad Request",
		})
	}))
	defer server.Close()

	if _, err := newTestGoogle(server).Exchange(context.Background(), "the-code", testGoogleRedirectURI); err == nil {
		t.Fatal("a refused code was accepted")
	}
}
