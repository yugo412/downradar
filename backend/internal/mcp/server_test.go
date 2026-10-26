package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"downradar.info/backend/internal/store"
)

type fakeReader struct {
	vendors     []store.VendorSummary
	details     map[string]*store.VendorDetail
	changes     []store.ChangeView
	vendorsErr  error
	detailsErr  error
	changesErr  error
	changeLimit int
}

func (f *fakeReader) ListVendors(context.Context) ([]store.VendorSummary, error) {
	return f.vendors, f.vendorsErr
}

func (f *fakeReader) GetVendor(_ context.Context, slug string, _ store.Viewer) (*store.VendorDetail, error) {
	if f.detailsErr != nil {
		return nil, f.detailsErr
	}
	detail, ok := f.details[slug]
	if !ok {
		return nil, store.ErrNotFound
	}
	return detail, nil
}

func (f *fakeReader) RecentChanges(_ context.Context, limit int) ([]store.ChangeView, error) {
	f.changeLimit = limit
	return f.changes, f.changesErr
}

func sampleReader() *fakeReader {
	changed := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	polled := changed.Add(5 * time.Minute)
	return &fakeReader{
		vendors: []store.VendorSummary{
			{Slug: "elastic", Name: "Elastic", URL: "https://status.elastic.co", Status: "partial", ChangedAt: &changed, Components: 2, Degraded: 1, LastPolledAt: &polled},
			{Slug: "github", Name: "GitHub", URL: "https://www.githubstatus.com", Status: "ok", ChangedAt: &changed, Components: 3, LastPolledAt: &polled},
			{Slug: "strava", Name: "Strava", URL: "https://status.strava.com", Status: "ok", ChangedAt: &changed, Components: 1, LastPolledAt: &polled},
		},
		details: map[string]*store.VendorDetail{
			"elastic": {
				VendorSummary: store.VendorSummary{
					Slug: "elastic", Name: "Elastic", URL: "https://status.elastic.co",
					Status: "partial", ChangedAt: &changed, Components: 2, Degraded: 1,
					LastPolledAt: &polled,
				},
				ComponentsList: []store.ComponentView{
					{SourceID: "search", Name: "Search", Position: 1, Status: "ok", ChangedAt: &changed},
					{SourceID: "kibana", Name: "Kibana", Position: 2, Status: "partial", ChangedAt: &changed},
				},
				RecentEvents: []store.EventView{
					{ID: 12, Status: "partial", Previous: "ok", Component: "Kibana", ChangedAt: changed},
				},
				Incidents: []store.IncidentView{
					{SourceID: "incident-1", Name: "Kibana latency", Status: "investigating", Impact: "minor", StartedAt: &changed},
				},
			},
		},
		changes: []store.ChangeView{
			{ID: 12, VendorSlug: "elastic", VendorName: "Elastic", Component: "Kibana", From: "ok", To: "partial", ChangedAt: changed},
		},
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func connect(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()

	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() {
		if err := clientSession.Close(); err != nil {
			t.Errorf("client close: %v", err)
		}
		if err := serverSession.Wait(); err != nil {
			t.Errorf("server wait: %v", err)
		}
	})
	return clientSession
}

func decodeContent[T any](t *testing.T, result *sdk.CallToolResult) T {
	t.Helper()

	if result.IsError {
		t.Fatalf("tool returned an error: %s", textContent(t, result))
	}
	if len(result.Content) != 1 {
		t.Fatalf("content count = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *mcp.TextContent", result.Content[0])
	}

	var out T
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode content: %v\n%s", err, text.Text)
	}
	return out
}

func textContent(t *testing.T, result *sdk.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		return ""
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}

func callTool(t *testing.T, session *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestToolListIsReadOnly(t *testing.T) {
	session := connect(t, NewServer(sampleReader(), quietLogger()))

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	if len(result.Tools) != 4 {
		t.Fatalf("tool count = %d, want 4", len(result.Tools))
	}
	seen := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		seen[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read only", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no output schema", tool.Name)
		}
	}
	for _, name := range []string{"get_vendor", "list_vendors", "recent_changes", "search_vendors"} {
		if !seen[name] {
			t.Errorf("tool %s is missing", name)
		}
	}
}

func TestListVendors(t *testing.T) {
	session := connect(t, NewServer(sampleReader(), quietLogger()))

	out := decodeContent[listVendorsOutput](t, callTool(t, session, "list_vendors", map[string]any{}))
	if out.Count != 3 || len(out.Vendors) != 3 {
		t.Fatalf("count = %d, vendors = %d, want 3", out.Count, len(out.Vendors))
	}
	if out.Vendors[0].Slug != "elastic" {
		t.Errorf("first vendor = %q, want elastic", out.Vendors[0].Slug)
	}
}

func TestSearchVendors(t *testing.T) {
	session := connect(t, NewServer(sampleReader(), quietLogger()))

	name := decodeContent[searchVendorsOutput](t, callTool(t, session, "search_vendors", map[string]any{"query": "GIT"}))
	if name.Matched != 1 || name.Count != 1 || name.Vendors[0].Slug != "github" {
		t.Errorf("name search = %+v, want only github", name)
	}

	status := decodeContent[searchVendorsOutput](t, callTool(t, session, "search_vendors", map[string]any{"status": "ok"}))
	if status.Matched != 2 || status.Count != 2 {
		t.Errorf("status search count = %d, matched = %d, want 2", status.Count, status.Matched)
	}

	limited := decodeContent[searchVendorsOutput](t, callTool(t, session, "search_vendors", map[string]any{"status": "ok", "limit": 1}))
	if limited.Matched != 2 || limited.Count != 1 || len(limited.Vendors) != 1 {
		t.Errorf("limited search = %+v, want 1 of 2", limited)
	}

	errorResult := callTool(t, session, "search_vendors", map[string]any{"status": "sleepy"})
	if !errorResult.IsError {
		t.Fatalf("invalid status was accepted")
	}
	if text := textContent(t, errorResult); !strings.Contains(text, "unknown status") {
		t.Errorf("error text = %q, want an unknown status message", text)
	}
}

func TestGetVendor(t *testing.T) {
	session := connect(t, NewServer(sampleReader(), quietLogger()))

	out := decodeContent[store.VendorDetail](t, callTool(t, session, "get_vendor", map[string]any{"slug": "elastic"}))
	if out.Slug != "elastic" || len(out.ComponentsList) != 2 || len(out.RecentEvents) != 1 || len(out.Incidents) != 1 {
		t.Fatalf("detail = %+v, want elastic with components, events and incidents", out)
	}

	missing := callTool(t, session, "get_vendor", map[string]any{"slug": "nope"})
	if !missing.IsError {
		t.Fatalf("missing vendor was accepted")
	}
	if text := textContent(t, missing); !strings.Contains(text, "not found") {
		t.Errorf("error text = %q, want a not found message", text)
	}
}

func TestRecentChanges(t *testing.T) {
	reader := sampleReader()
	session := connect(t, NewServer(reader, quietLogger()))

	out := decodeContent[recentChangesOutput](t, callTool(t, session, "recent_changes", map[string]any{}))
	if out.Count != 1 || out.Changes[0].VendorSlug != "elastic" {
		t.Fatalf("changes = %+v, want the elastic change", out)
	}
	if reader.changeLimit != defaultChangesLimit {
		t.Errorf("store limit = %d, want default %d", reader.changeLimit, defaultChangesLimit)
	}

	callTool(t, session, "recent_changes", map[string]any{"limit": 1000})
	if reader.changeLimit != maxChangesLimit {
		t.Errorf("store limit = %d, want cap %d", reader.changeLimit, maxChangesLimit)
	}
}

func TestStoreFailureBecomesToolError(t *testing.T) {
	reader := sampleReader()
	reader.vendorsErr = errors.New("database is down")
	session := connect(t, NewServer(reader, quietLogger()))

	result := callTool(t, session, "list_vendors", map[string]any{})
	if !result.IsError {
		t.Fatalf("store failure was accepted")
	}
	if strings.Contains(textContent(t, result), "database is down") {
		t.Errorf("tool error leaks the store error")
	}
}

func TestHTTPHandlerAnswersJSON(t *testing.T) {
	handler := NewHTTPHandler(sampleReader(), quietLogger())
	req := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("content type = %q, want application/json", got)
	}
	if !strings.Contains(rec.Body.String(), `"tools"`) {
		t.Errorf("body has no tools: %s", rec.Body.String())
	}
}

func TestHTTPHandlerSpeaksJSON(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(sampleReader(), quietLogger()))
	t.Cleanup(server.Close)

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           server.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 4 {
		t.Fatalf("tool count = %d, want 4", len(result.Tools))
	}

	out := decodeContent[recentChangesOutput](t, callTool(t, session, "recent_changes", map[string]any{}))
	if out.Count != 1 {
		t.Errorf("change count = %d, want 1", out.Count)
	}
}

func TestHTTPHandlerServesConcurrentCallers(t *testing.T) {
	server := httptest.NewServer(NewHTTPHandler(sampleReader(), quietLogger()))
	t.Cleanup(server.Close)

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
			session, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
				Endpoint:             server.URL,
				HTTPClient:           server.Client(),
				DisableStandaloneSSE: true,
			}, nil)
			if err != nil {
				errs <- err
				return
			}
			defer session.Close()

			result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
				Name:      "list_vendors",
				Arguments: map[string]any{},
			})
			if err != nil {
				errs <- err
				return
			}
			if result.IsError {
				errs <- result.GetError()
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("caller: %v", err)
	}
}
