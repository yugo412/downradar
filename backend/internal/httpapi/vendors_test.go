package httpapi

import (
	"net/http"
	"testing"
)

// An alert link opens the vendor page, and the reader compares the message with
// what the page says now. The detail is therefore never stored: a shared cache
// would answer with a state from up to a minute ago and make them refresh.
func TestVendorDetailIsNotCached(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("vendor detail = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for a vendor page", got)
	}
}
