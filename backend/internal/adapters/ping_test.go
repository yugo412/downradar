package adapters

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// servePing answers with the given status code and checks that the request is
// the plain one a ping makes.
func servePing(t *testing.T, status int) (*httptest.Server, *Ping) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("User-Agent"); got != UserAgent {
			t.Errorf("User-Agent = %q, want %q", got, UserAgent)
		}
		w.WriteHeader(status)
		if status < 300 {
			_, _ = w.Write([]byte("<html><body>hello</body></html>"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, NewPing(srv.Client())
}

func TestPingFetchReadsTheAnswer(t *testing.T) {
	srv, adapter := servePing(t, http.StatusOK)

	rep, err := adapter.Fetch(context.Background(), Target{
		Slug: "example", URL: srv.URL, Kind: KindPing,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if rep.Global != HealthOK {
		t.Errorf("Global = %q, want ok", rep.Global)
	}
	if rep.HTTPStatus != http.StatusOK {
		t.Errorf("HTTPStatus = %d, want 200", rep.HTTPStatus)
	}
	if len(rep.Raw) != 0 {
		t.Errorf("Raw = %q, want the body left unread", rep.Raw)
	}

	host := strings.TrimPrefix(srv.URL, "http://")
	if len(rep.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(rep.Components))
	}
	got := rep.Components[0]
	if got.SourceID != host || got.Name != host {
		t.Errorf("component = %q/%q, want %q twice", got.SourceID, got.Name, host)
	}
	if got.Status != HealthOK {
		t.Errorf("component status = %q, want ok", got.Status)
	}
}

func TestPingFetchMapsTheStatusCode(t *testing.T) {
	cases := []struct {
		code int
		want Health
	}{
		{http.StatusOK, HealthOK},
		{http.StatusNoContent, HealthOK},
		{http.StatusMovedPermanently, HealthOK},
		{http.StatusForbidden, HealthUnknown},
		{http.StatusNotFound, HealthUnknown},
		{http.StatusTooManyRequests, HealthUnknown},
		{http.StatusInternalServerError, HealthMajor},
		{http.StatusServiceUnavailable, HealthMajor},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("HTTP %d", tc.code), func(t *testing.T) {
			srv, adapter := servePing(t, tc.code)

			rep, err := adapter.Fetch(context.Background(), Target{URL: srv.URL, Kind: KindPing})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if rep.Global != tc.want {
				t.Errorf("Global = %q, want %q", rep.Global, tc.want)
			}
			if rep.HTTPStatus != tc.code {
				t.Errorf("HTTPStatus = %d, want %d", rep.HTTPStatus, tc.code)
			}
			if len(rep.Components) != 1 || rep.Components[0].Status != tc.want {
				t.Errorf("components = %+v, want one at %q", rep.Components, tc.want)
			}
		})
	}
}

func TestPingFetchReportsSilenceAsDown(t *testing.T) {
	srv, _ := servePing(t, http.StatusOK)
	url := srv.URL
	srv.Close() // nothing is listening at the address any more

	rep, err := NewPing(nil).Fetch(context.Background(), Target{Slug: "gone", URL: url, Kind: KindPing})
	if err != nil {
		t.Fatalf("Fetch: %v, want the silence taken as the report", err)
	}

	if rep.Global != HealthMajor {
		t.Errorf("Global = %q, want major", rep.Global)
	}
	if rep.HTTPStatus != 0 {
		t.Errorf("HTTPStatus = %d, want 0 when nothing answered", rep.HTTPStatus)
	}
	if len(rep.Components) != 1 || rep.Components[0].Status != HealthMajor {
		t.Errorf("components = %+v, want one at major", rep.Components)
	}
}

func TestPingRejectsWhatIsNotAnHTTPAddress(t *testing.T) {
	for _, url := range []string{"", "ftp://example.com/x", "example.com"} {
		t.Run(url, func(t *testing.T) {
			if _, err := NewPing(nil).Fetch(context.Background(), Target{URL: url}); err == nil {
				t.Errorf("Fetch(%q) accepted a url that is not an http address", url)
			}
		})
	}
}
