package lark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func serveLark(t *testing.T, body string) (*httptest.Server, *Lark) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/service/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, NewLark(srv.Client())
}

func TestLarkFetchRealFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "lark_status.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveLark(t, string(body))
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		Slug: "lark", URL: srv.URL, Kind: KindLark,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	want := []string{
		"Messenger", "Search", "Calendar", "Contacts", "Meetings", "Docs",
		"Email", "Workplace", "Admin console", "Log in", "Open API", "Approval",
		"Clock in",
	}
	if len(rep.Components) != len(want) {
		t.Fatalf("components = %d, want %d", len(rep.Components), len(want))
	}
	for i, name := range want {
		got := rep.Components[i]
		if got.Name != name {
			t.Errorf("component %d name = %q, want %q", i, got.Name, name)
		}
		if got.Status != adapters.HealthOK {
			t.Errorf("component %d status = %q, want ok", i, got.Status)
		}
		if got.Position != i+1 {
			t.Errorf("component %d position = %d, want %d", i, got.Position, i+1)
		}
	}
	if rep.Components[3].SourceID != "18" {
		t.Errorf("Contacts source id = %q, want 18", rep.Components[3].SourceID)
	}
}

func TestLarkStatusVocabulary(t *testing.T) {
	cases := []struct {
		name string
		raw  int
		want adapters.Health
	}{
		{"operational", 0, adapters.HealthOK},
		{"maintenance", 10, adapters.HealthMaintenance},
		{"incident", 20, adapters.HealthPartial},
		{"outage", 99, adapters.HealthMajor},
		{"unknown value", 7, adapters.HealthUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveLark(t,
				`{"code":0,"data":{"serviceList":[{"bizType":1,"status":`+strconv.Itoa(tc.raw)+`}]}}`)
			rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != tc.want {
				t.Errorf("Global = %q, want %q", rep.Global, tc.want)
			}
			if len(rep.Components) != 1 || rep.Components[0].Status != tc.want {
				t.Errorf("components = %+v, want %q", rep.Components, tc.want)
			}
		})
	}
}

func TestLarkMissingStatusIsUnknown(t *testing.T) {
	srv, adapter := serveLark(t,
		`{"code":0,"data":{"serviceList":[{"bizType":1}]}}`)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthUnknown || rep.Components[0].Status != adapters.HealthUnknown {
		t.Errorf("missing status was read as %q, want unknown", rep.Components[0].Status)
	}
}

func TestLarkGlobalIsTheWorstService(t *testing.T) {
	srv, adapter := serveLark(t,
		`{"code":0,"data":{"serviceList":[{"bizType":1,"status":0},{"bizType":4,"status":20},{"bizType":5,"status":99}]}}`)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major", rep.Global)
	}
}

func TestLarkIncludeFilter(t *testing.T) {
	srv, adapter := serveLark(t,
		`{"code":0,"data":{"serviceList":[{"bizType":1,"status":0},{"bizType":4,"status":20}]}}`)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{
		URL: srv.URL, Include: []string{"  MEETINGS  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want only the one asked for", len(rep.Components))
	}
	if rep.Components[0].Name != "Meetings" || rep.Components[0].Status != adapters.HealthPartial {
		t.Errorf("component = %+v", rep.Components[0])
	}
	if rep.Components[0].Position != 2 {
		t.Errorf("position = %d, want its place on the page (2)", rep.Components[0].Position)
	}
	if rep.Global != adapters.HealthPartial {
		t.Errorf("Global = %q, want the unfiltered page status", rep.Global)
	}
}

func TestLarkUnknownServiceKeepsItsNumber(t *testing.T) {
	srv, adapter := serveLark(t,
		`{"code":0,"data":{"serviceList":[{"bizType":42,"status":0}]}}`)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if rep.Components[0].Name != "Service 42" || rep.Components[0].SourceID != "42" {
		t.Errorf("component = %+v", rep.Components[0])
	}
}

func TestLarkRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"api error", `{"code":100,"message":"not found","data":{"serviceList":[]}}`, "code 100"},
		{"no services", `{"code":0,"data":{"serviceList":[]}}`, "lists no services"},
		{"not json", `<html>nope</html>`, "decode json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, adapter := serveLark(t, tc.body)
			_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLarkHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	rep, err := NewLark(srv.Client()).Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error on HTTP 429")
	}
	if rep.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus = %d, want 429", rep.HTTPStatus)
	}
}
