package suggest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateIssueSendsLabels(t *testing.T) {
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

	client := NewClient(srv.URL, "acme/status", "tok", []string{"suggestion"})
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
	if len(gotBody.Labels) != 1 || gotBody.Labels[0] != "suggestion" {
		t.Errorf("labels = %v, want [suggestion]", gotBody.Labels)
	}
}

func TestCreateIssueRetriesWithoutLabelsWhenRejected(t *testing.T) {
	var calls int32
	var secondBody createIssueRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&calls, 1)
		if attempt == 1 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Issue","field":"labels","code":"invalid"}]}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&secondBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/acme/status/issues/7"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok", []string{"suggestion"})
	url, err := client.CreateIssue(context.Background(), "title", "body")
	if err != nil {
		t.Fatalf("a rejected label must not lose the suggestion: %v", err)
	}
	if url != "https://github.com/acme/status/issues/7" {
		t.Errorf("url = %q", url)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected a retry, got %d calls", calls)
	}
	if len(secondBody.Labels) != 0 {
		t.Errorf("the retry should drop the labels, got %v", secondBody.Labels)
	}
}

func TestCreateIssueDoesNotRetryOnOtherValidationErrors(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Issue","field":"title","code":"missing"}]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok", []string{"suggestion"})
	if _, err := client.CreateIssue(context.Background(), "title", "body"); err == nil {
		t.Fatal("expected an error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("only a label rejection should be retried, got %d calls", calls)
	}
}

func TestCreateIssueSurfacesGitHubErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "bad", nil)
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

	client := NewClient(srv.URL, "acme/status", "tok", nil)
	if _, err := client.CreateIssue(context.Background(), "title", "body"); err == nil {
		t.Fatal("expected an error when html_url is missing")
	}
}

func TestEnsureLabels(t *testing.T) {
	var created []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/status/labels" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body createLabelRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		created = append(created, body.Name)

		if body.Name == "exists" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"field":"name","code":"already_exists"}]}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"` + body.Name + `"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok", []string{"suggestion", "exists"})
	if err := client.EnsureLabels(context.Background()); err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if len(created) != 2 {
		t.Errorf("created = %v, want two attempts", created)
	}
}

func TestEnsureLabelsReportsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "acme/status", "tok", []string{"suggestion"})
	err := client.EnsureLabels(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "suggestion") {
		t.Errorf("error = %q, want it to name the label", err)
	}
}

func TestEnsureLabelsDoesNothingWhenNotConfigured(t *testing.T) {
	client := NewClient("", "", "", []string{"suggestion"})
	if err := client.EnsureLabels(context.Background()); err != nil {
		t.Errorf("EnsureLabels without a token should be a no-op, got %v", err)
	}
}

func TestNotConfigured(t *testing.T) {
	for _, tc := range []struct{ repo, token string }{
		{"", "tok"},
		{"acme/status", ""},
		{"", ""},
	} {
		client := NewClient("", tc.repo, tc.token, nil)
		if client.CanSubmit() {
			t.Errorf("repo=%q token=%q should not be submittable", tc.repo, tc.token)
		}
		if _, err := client.CreateIssue(context.Background(), "t", "b"); err == nil {
			t.Error("expected an error when not configured")
		}
	}
}

func TestManualURL(t *testing.T) {
	client := NewClient("", "acme/status", "", []string{"suggestion"})

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
	if !strings.Contains(url, "labels=suggestion") {
		t.Errorf("url is missing the label: %q", url)
	}

	if got := NewClient("", "", "tok", nil).ManualURL("t", "b"); got != "" {
		t.Errorf("without a repo there is no manual url, got %q", got)
	}
}

func TestLabelsAreTrimmed(t *testing.T) {
	client := NewClient("", "acme/status", "tok", []string{" suggestion ", "", "   ", "bug"})
	labels := client.Labels()
	if len(labels) != 2 || labels[0] != "suggestion" || labels[1] != "bug" {
		t.Errorf("labels = %v, want [suggestion bug]", labels)
	}
}

func TestTrailingSlashesAreTolerated(t *testing.T) {
	client := NewClient("https://api.github.com/", "/acme/status/", "tok", nil)
	if client.baseURL != "https://api.github.com" {
		t.Errorf("baseURL = %q", client.baseURL)
	}
	if client.repo != "acme/status" {
		t.Errorf("repo = %q", client.repo)
	}
}
