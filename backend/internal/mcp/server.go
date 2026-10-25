// Package mcp exposes the public status catalogue over the Model Context
// Protocol. It is a read-only view of the same store the JSON API reads, so an
// assistant joins the public catalogue rather than becoming another source of
// truth.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"downradar.info/backend/internal/store"
)

const (
	defaultSearchLimit  = 20
	maxSearchLimit      = 100
	defaultChangesLimit = 20
	maxChangesLimit     = 100
)

var statusNames = map[string]bool{
	"ok":          true,
	"degraded":    true,
	"partial":     true,
	"major":       true,
	"maintenance": true,
	"unknown":     true,
}

// Reader is the part of the store the tools may use. Keeping it narrow is what
// makes the endpoint read only: there is no way to reach a write, an account or
// a management query through it.
type Reader interface {
	ListVendors(context.Context) ([]store.VendorSummary, error)
	GetVendor(context.Context, string, store.Viewer) (*store.VendorDetail, error)
	RecentChanges(context.Context, int) ([]store.ChangeView, error)
}

type tools struct {
	reader Reader
	log    *slog.Logger
}

// NewServer builds one MCP server. The streamable HTTP handler creates a
// temporary session per request, so the same server serves every caller.
func NewServer(reader Reader, log *slog.Logger) *sdk.Server {
	if log == nil {
		log = slog.Default()
	}

	// The SDK logs every session at info, and a stateless session is one HTTP
	// request. Leaving its logger unset keeps one call from writing two lines.
	t := &tools{reader: reader, log: log}
	server := sdk.NewServer(&sdk.Implementation{
		Name:        "downradar",
		Title:       "DownRadar status",
		Description: "Public status data from downradar.info.",
		Version:     "1.0.0",
		WebsiteURL:  "https://downradar.info",
	}, &sdk.ServerOptions{
		Instructions: "DownRadar collects the current status of public services " +
			"from their official status pages. Use list_vendors or search_vendors " +
			"to find a service, get_vendor for its components, recent events and " +
			"incidents, and recent_changes for the public change feed.",
	})

	sdk.AddTool(server, &sdk.Tool{
		Name:        "list_vendors",
		Title:       "List vendors",
		Description: "List every public vendor with its current overall status, component counts and last change time.",
		Annotations: readOnly("List vendors"),
	}, t.listVendors)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "search_vendors",
		Title: "Search vendors",
		Description: "Find public vendors by name or slug and/or by exact current status. " +
			"Statuses are ok, degraded, partial, major, maintenance and unknown. " +
			"Returns up to 20 matches by default; set limit up to 100.",
		Annotations: readOnly("Search vendors"),
	}, t.searchVendors)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_vendor",
		Title:       "Get vendor status",
		Description: "Read one public vendor's current status, components, recent status events and incidents. Use a slug from list_vendors or search_vendors.",
		Annotations: readOnly("Get vendor status"),
	}, t.getVendor)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "recent_changes",
		Title:       "Recent changes",
		Description: "Read the newest status changes in the public site feed, newest first. First sightings are not included; use get_vendor for one vendor's own history.",
		Annotations: readOnly("Recent changes"),
	}, t.recentChanges)

	return server
}

// NewHTTPHandler mounts the server as a stateless Streamable HTTP endpoint. The
// JSON response mode is deliberate: no caller needs a server-initiated stream,
// and every answer fits in one response.
func NewHTTPHandler(reader Reader, log *slog.Logger) http.Handler {
	server := NewServer(reader, log)
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{
			Stateless:           true,
			JSONResponse:        true,
			Logger:              log,
			MaxRequestBodyBytes: 1 << 20,
		},
	)
}

func readOnly(title string) *sdk.ToolAnnotations {
	open := false
	return &sdk.ToolAnnotations{
		Title:          title,
		ReadOnlyHint:   true,
		IdempotentHint: true,
		OpenWorldHint:  &open,
	}
}

type listVendorsOutput struct {
	Vendors []store.VendorSummary `json:"vendors"`
	Count   int                   `json:"count"`
}

func (t *tools) listVendors(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, listVendorsOutput, error) {
	vendors, err := t.reader.ListVendors(ctx)
	if err != nil {
		t.log.Error("mcp list vendors failed", "error", err)
		return nil, listVendorsOutput{}, errors.New("vendor data is temporarily unavailable")
	}
	if vendors == nil {
		vendors = []store.VendorSummary{}
	}
	return nil, listVendorsOutput{Vendors: vendors, Count: len(vendors)}, nil
}

type searchVendorsInput struct {
	Query  string `json:"query,omitempty" jsonschema:"name or slug text to match, case insensitive"`
	Status string `json:"status,omitempty" jsonschema:"exact status to match: ok, degraded, partial, major, maintenance or unknown"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum matches to return, from 1 to 100; defaults to 20"`
}

type searchVendorsOutput struct {
	Vendors []store.VendorSummary `json:"vendors"`
	Count   int                   `json:"count"`
	Matched int                   `json:"matched"`
}

func (t *tools) searchVendors(ctx context.Context, _ *sdk.CallToolRequest, in searchVendorsInput) (*sdk.CallToolResult, searchVendorsOutput, error) {
	query := strings.ToLower(strings.TrimSpace(in.Query))
	status := strings.ToLower(strings.TrimSpace(in.Status))
	if status != "" && !statusNames[status] {
		return nil, searchVendorsOutput{}, fmt.Errorf("unknown status %q; use ok, degraded, partial, major, maintenance or unknown", status)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	vendors, err := t.reader.ListVendors(ctx)
	if err != nil {
		t.log.Error("mcp search vendors failed", "error", err)
		return nil, searchVendorsOutput{}, errors.New("vendor data is temporarily unavailable")
	}

	matches := make([]store.VendorSummary, 0, len(vendors))
	for _, vendor := range vendors {
		if status != "" && vendor.Status != status {
			continue
		}
		if query != "" {
			name := strings.ToLower(vendor.Name)
			slug := strings.ToLower(vendor.Slug)
			if !strings.Contains(name, query) && !strings.Contains(slug, query) {
				continue
			}
		}
		matches = append(matches, vendor)
	}

	matched := len(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return nil, searchVendorsOutput{Vendors: matches, Count: len(matches), Matched: matched}, nil
}

type getVendorInput struct {
	Slug string `json:"slug" jsonschema:"the vendor's stable slug, as returned by list_vendors or search_vendors"`
}

func (t *tools) getVendor(ctx context.Context, _ *sdk.CallToolRequest, in getVendorInput) (*sdk.CallToolResult, store.VendorDetail, error) {
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		return nil, store.VendorDetail{}, errors.New("slug is required")
	}

	detail, err := t.reader.GetVendor(ctx, slug, store.Public())
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.VendorDetail{}, fmt.Errorf("vendor %q was not found", slug)
	}
	if err != nil {
		t.log.Error("mcp get vendor failed", "slug", slug, "error", err)
		return nil, store.VendorDetail{}, errors.New("vendor data is temporarily unavailable")
	}
	return nil, *detail, nil
}

type recentChangesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum changes to return, from 1 to 100; defaults to 20"`
}

type recentChangesOutput struct {
	Changes []store.ChangeView `json:"changes"`
	Count   int                `json:"count"`
}

func (t *tools) recentChanges(ctx context.Context, _ *sdk.CallToolRequest, in recentChangesInput) (*sdk.CallToolResult, recentChangesOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultChangesLimit
	}
	if limit > maxChangesLimit {
		limit = maxChangesLimit
	}

	changes, err := t.reader.RecentChanges(ctx, limit)
	if err != nil {
		t.log.Error("mcp recent changes failed", "error", err)
		return nil, recentChangesOutput{}, errors.New("change data is temporarily unavailable")
	}
	if changes == nil {
		changes = []store.ChangeView{}
	}
	return nil, recentChangesOutput{Changes: changes, Count: len(changes)}, nil
}
