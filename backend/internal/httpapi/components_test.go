package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

type componentListAnswer struct {
	Components []store.ComponentView `json:"components"`
}

// Staff see the component list with hidden rows included, can take one off the
// public page, and can put it back. Everyone else sees the public page and
// cannot call either endpoint.
func TestStaffHideAComponentFromThePublicPage(t *testing.T) {
	st, router := testRouter(t)
	ctx := context.Background()
	slug := alertVendor(t, st)

	id, _, err := st.PublicService(ctx, slug)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	report := adapters.Report{
		Global:    adapters.HealthOK,
		FetchedAt: time.Now().UTC(),
		Components: []adapters.ComponentStatus{
			{SourceID: "web", Name: "Web", Position: 1, Status: adapters.HealthOK},
			{SourceID: "noise", Name: "Visit the page for more information", Position: 2, Status: adapters.HealthOK},
		},
	}
	if _, err := st.ApplyReport(ctx, id, report); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	publicDetail := func() store.VendorDetail {
		t.Helper()
		rec := call(t, router, http.MethodGet, "/api/v1/vendors/"+slug, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("public vendor = %d: %s", rec.Code, rec.Body.String())
		}
		var detail store.VendorDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatalf("decode public vendor: %v", err)
		}
		return detail
	}
	staffList := func(cookie string) componentListAnswer {
		t.Helper()
		rec := call(t, router, http.MethodGet, "/api/v1/services/"+slug+"/components", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("staff components = %d: %s", rec.Code, rec.Body.String())
		}
		var answer componentListAnswer
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("decode staff components: %v", err)
		}
		return answer
	}

	if got := len(publicDetail().ComponentsList); got != 2 {
		t.Fatalf("public components = %d, want both before hiding", got)
	}

	subscriber := testUser(t, st, auth.RoleSubscriber)
	subscriberCookie := sessionFor(t, st, subscriber)
	if rec := call(t, router, http.MethodGet, "/api/v1/services/"+slug+"/components", "", subscriberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a subscriber read the staff list = %d, want 403", rec.Code)
	}
	if rec := call(t, router, http.MethodPatch, "/api/v1/services/"+slug+"/components",
		`{"sourceId":"noise","hidden":true}`, subscriberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a subscriber hid a component = %d, want 403", rec.Code)
	}

	staff := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, staff)

	if got := len(staffList(cookie).Components); got != 2 {
		t.Fatalf("staff components = %d, want both", got)
	}

	rec := call(t, router, http.MethodPatch, "/api/v1/services/"+slug+"/components",
		`{"sourceId":"noise","hidden":true}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("hiding a component = %d: %s", rec.Code, rec.Body.String())
	}

	detail := publicDetail()
	if len(detail.ComponentsList) != 1 || detail.ComponentsList[0].SourceID != "web" {
		t.Errorf("public list = %+v, want only web", detail.ComponentsList)
	}

	list := staffList(cookie)
	if len(list.Components) != 2 {
		t.Fatalf("staff components = %d, want the hidden one too", len(list.Components))
	}
	hidden := false
	for _, c := range list.Components {
		if c.SourceID == "noise" && c.Hidden {
			hidden = true
		}
	}
	if !hidden {
		t.Error("the staff list does not mark the hidden component")
	}

	rec = call(t, router, http.MethodPatch, "/api/v1/services/"+slug+"/components",
		`{"sourceId":"missing","hidden":true}`, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("hiding an unknown component = %d, want 404", rec.Code)
	}

	rec = call(t, router, http.MethodPatch, "/api/v1/services/"+slug+"/components",
		`{"sourceId":"noise","hidden":false}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("showing the component again = %d: %s", rec.Code, rec.Body.String())
	}
	if got := len(publicDetail().ComponentsList); got != 2 {
		t.Errorf("public components after showing = %d, want both", got)
	}
}

func TestHidingAComponentRefusesAnEmptyID(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)
	staff := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, staff)

	rec := call(t, router, http.MethodPatch, "/api/v1/services/"+slug+"/components",
		`{"hidden":true}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an empty component id = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestStaffComponentListNeedsAServiceThatExists(t *testing.T) {
	st, router := testRouter(t)
	staff := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, staff)

	rec := call(t, router, http.MethodGet, "/api/v1/services/no-such-service/components", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("components of a missing service = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
