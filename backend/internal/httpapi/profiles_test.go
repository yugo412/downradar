package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/catalog"
	"downradar.info/backend/internal/store"
)

func profileTestCatalog() catalog.Catalog {
	return catalog.Catalog{
		Profiles: catalog.Profiles{
			"statuspage-v2": {
				Kind:                  "statuspage",
				Description:           "Pages hosted by Atlassian Statuspage.",
				Endpoint:              "/api/v2/summary.json",
				PollIntervalSeconds:   300,
				ActiveIntervalSeconds: 60,
				Visible:               true,
			},
			"statusiq": {
				Kind:                  "statusiq",
				Description:           "Pages built with StatusIQ.",
				Endpoint:              "/sp/api/",
				PollIntervalSeconds:   300,
				ActiveIntervalSeconds: 60,
				Visible:               true,
			},
			"unused": {
				Kind:                  "statuspage",
				PollIntervalSeconds:   600,
				ActiveIntervalSeconds: 120,
			},
		},
		Detect: []string{"statuspage-v2", "statusiq"},
	}
}

// isolatedProfileRouter gives the count test a schema of its own, so a service
// added by another package at the same time cannot change the count.
func isolatedProfileRouter(t *testing.T, tune func(*Options)) (*store.Store, http.Handler) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	schema := fmt.Sprintf("zz_profiles_http_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `create schema `+schema); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `drop schema `+schema+` cascade`)
		_ = conn.Close(context.Background())
	})

	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	st, router, _ := testRouterWithDSN(t, dsn+separator+"search_path="+schema, tune)
	return st, router
}

func createProfileService(t *testing.T, st *store.Store, profile string) {
	t.Helper()

	slug, err := st.CreateService(context.Background(), store.NewService{
		Name:                  fmt.Sprintf("Zz Profile Vendor %d", time.Now().UnixNano()),
		URL:                   "https://status.example.com",
		Profile:               profile,
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteService(context.Background(), slug) })
}

func TestStaffProfilesCountsTheServicesOnEachProfile(t *testing.T) {
	st, router := isolatedProfileRouter(t, func(o *Options) {
		o.Catalog = profileTestCatalog()
	})

	admin := testUser(t, st, auth.RoleAdmin)
	cookie := sessionFor(t, st, admin)

	createProfileService(t, st, "statuspage-v2")
	createProfileService(t, st, "statuspage-v2")
	createProfileService(t, st, "statusiq")

	rec := call(t, router, http.MethodGet, "/api/v1/staff/profiles", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("profiles = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Profiles []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			Services int    `json:"services"`
			Detect   bool   `json:"detect"`
			Visible  bool   `json:"visible"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Profiles) != 3 {
		t.Fatalf("profiles = %d, want every profile in the file", len(body.Profiles))
	}

	// The list is by name, and every profile is there even with no service.
	wantOrder := []string{"statusiq", "statuspage-v2", "unused"}
	for i, name := range wantOrder {
		if body.Profiles[i].Name != name {
			t.Errorf("profile %d = %q, want %q", i, body.Profiles[i].Name, name)
		}
	}

	byName := make(map[string]int, len(body.Profiles))
	for i, p := range body.Profiles {
		byName[p.Name] = i
	}

	sp := body.Profiles[byName["statuspage-v2"]]
	if sp.Services != 2 || !sp.Detect || !sp.Visible || sp.Kind != "statuspage" {
		t.Errorf("statuspage-v2 = %+v, want two services, detection and the picker", sp)
	}
	si := body.Profiles[byName["statusiq"]]
	if si.Services != 1 || !si.Detect {
		t.Errorf("statusiq = %+v, want one service and detection", si)
	}
	unused := body.Profiles[byName["unused"]]
	if unused.Services != 0 || unused.Detect || unused.Visible {
		t.Errorf("unused = %+v, want no services, no detection and hidden", unused)
	}
}

func TestStaffProfilesIsStaffOnly(t *testing.T) {
	st, router := isolatedProfileRouter(t, func(o *Options) {
		o.Catalog = profileTestCatalog()
	})

	subscriber := testUser(t, st, auth.RoleSubscriber)
	rec := call(t, router, http.MethodGet, "/api/v1/staff/profiles", "", sessionFor(t, st, subscriber))
	if rec.Code != http.StatusForbidden {
		t.Errorf("subscriber = %d, want 403", rec.Code)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/staff/profiles", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", rec.Code)
	}
}
