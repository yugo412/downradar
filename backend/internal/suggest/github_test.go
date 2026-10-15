package suggest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateIssue(t *testing.T) {
	var gotAuth, gotAccept, gotPath string
	var gotBody createIssueRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/acme/status/issues/42","number":42}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok")
	if !client.CanSubmit() {
		t.Fatal("client should be able to submit")
	}

	url, err := client.CreateIssue(context.Background(), "[suggestion] Foo", "body text")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if url != "https://github.com/acme/status/issues/42" {
		t.Errorf("url = %q", url)
	}
	if gotPath != "/repos/acme/status/issues" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotBody.Title != "[suggestion] Foo" || gotBody.Body != "body text" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestCreateIssueSurfacesGitHubErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "bad")
	_, err := client.CreateIssue(context.Background(), "title", "body")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want it to mention 401", err)
	}
}

func TestCreateIssueRejectsMissingHTMLURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok")
	if _, err := client.CreateIssue(context.Background(), "title", "body"); err == nil {
		t.Fatal("expected an error when html_url is missing")
	}
}

func TestNotConfigured(t *testing.T) {
	for _, tc := range []struct{ repo, token string }{
		{"", "tok"},
		{"acme/status", ""},
		{"", ""},
	} {
		client := NewClient("", tc.repo, tc.token)
		if client.CanSubmit() {
			t.Errorf("repo=%q token=%q should not be submittable", tc.repo, tc.token)
		}
		if _, err := client.CreateIssue(context.Background(), "t", "b"); err == nil {
			t.Error("expected an error when not configured")
		}
	}
}

func TestManualURL(t *testing.T) {
	client := NewClient("", "acme/status", "")

	url := client.ManualURL("[suggestion] Foo", "line one\nline two")
	if !strings.HasPrefix(url, "https://github.com/acme/status/issues/new?") {
		t.Fatalf("url = %q", url)
	}
	if !strings.Contains(url, "title=%5Bsuggestion%5D+Foo") {
		t.Errorf("url is missing the encoded title: %q", url)
	}
	if !strings.Contains(url, "body=") {
		t.Errorf("url is missing the body: %q", url)
	}

	if got := NewClient("", "", "tok").ManualURL("t", "b"); got != "" {
		t.Errorf("without a repo there is no manual url, got %q", got)
	}
}

func TestTrailingSlashesAreTolerated(t *testing.T) {
	client := NewClient("https://api.github.com/", "/acme/status/", "tok")
	if client.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q", client.baseURL)
	}
	if client.repo != "acme/status" {
		t.Errorf("repo = %q", client.repo)
	}
}
