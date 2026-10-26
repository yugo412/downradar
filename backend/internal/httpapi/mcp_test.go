package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPEndpointServesThePublicCatalogue(t *testing.T) {
	_, router := testRouter(t)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint:             server.URL + "/api/v1/mcp",
		HTTPClient:           server.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("tool count = %d, want 4", len(tools.Tools))
	}

	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "list_vendors",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call list_vendors: %v", err)
	}
	if result.IsError {
		t.Fatalf("list_vendors failed: %v", result.GetError())
	}
}

func TestMCPEndpointRateLimitsEachCaller(t *testing.T) {
	_, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.RateLimitPerMinute = 1
		opts.RateLimitBurst = 1
		opts.InternalToken = "mcp-test-token"
	})

	post := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Internal-Token", "mcp-test-token")
		req.Header.Set("X-Forwarded-For", ip)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("203.0.113.9"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := post("203.0.113.9"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from the same caller = %d, want 429", rec.Code)
	} else if got := rec.Header().Get("Retry-After"); got == "" {
		t.Errorf("rate limited response has no Retry-After header")
	}
	if rec := post("203.0.113.10"); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("a different caller shares the first caller's budget")
	}
}
