package summary

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOpenAIWriteSendsTheStoredPrompt(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"The API is degraded."}}]}`)
	}))
	defer server.Close()

	client := NewOpenAI("secret-key", server.URL+"/v1/", "gpt-6-luna", time.Second)
	got, err := client.Write(context.Background(), "be brief", "the state")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got != "The API is degraded." {
		t.Errorf("summary = %q", got)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotBody.Model != "gpt-6-luna" {
		t.Errorf("model = %q", gotBody.Model)
	}
	if len(gotBody.Messages) != 2 ||
		gotBody.Messages[0].Content != "be brief" ||
		gotBody.Messages[1].Content != "the state" {
		t.Errorf("messages = %+v", gotBody.Messages)
	}
}

func TestOpenAIWriteReportsAnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer server.Close()

	client := NewOpenAI("secret-key", server.URL+"/v1", "gpt-6-luna", time.Second)
	_, err := client.Write(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("Write succeeded on a 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("error = %v, want the status and provider message", err)
	}
}

func TestOpenAIWriteRejectsAnEmptyAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer server.Close()

	client := NewOpenAI("secret-key", server.URL+"/v1", "gpt-6-luna", time.Second)
	if _, err := client.Write(context.Background(), "system", "user"); err == nil {
		t.Fatal("Write succeeded without a choice")
	}
}
