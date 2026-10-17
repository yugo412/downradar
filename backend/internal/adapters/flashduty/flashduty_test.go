package flashduty

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downradar.info/backend/internal/adapters"
)

func rscPage(parts ...string) []byte {
	escaper := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><body><script>")
	for _, p := range parts {
		sb.WriteString(`self.__next_f.push([1,"`)
		sb.WriteString(escaper.Replace(p))
		sb.WriteString(`"])`)
	}
	sb.WriteString("</script></body></html>")
	return []byte(sb.String())
}

func serveFlashDuty(t *testing.T, body []byte) (*httptest.Server, *FlashDuty) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, NewFlashDuty(srv.Client())
}

func TestFlashDutyFetchRealFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "flashduty.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv, adapter := serveFlashDuty(t, body)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{Slug: "deepseek", URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rep.Components) != 5 {
		t.Fatalf("components = %d, want 5", len(rep.Components))
	}
	if rep.Global != adapters.HealthOK {
		t.Errorf("Global = %q, want %q", rep.Global, adapters.HealthOK)
	}
	if len(rep.Incidents) != 0 {
		t.Errorf("incidents = %d, want 0", len(rep.Incidents))
	}

	byName := map[string]adapters.ComponentStatus{}
	for _, c := range rep.Components {
		byName[c.Name] = c
		if c.Status != adapters.HealthOK {
			t.Errorf("%s: status = %q, want ok", c.Name, c.Status)
		}
		if c.SourceID == "" {
			t.Errorf("%s: empty source id", c.Name)
		}
	}

	sectioned := byName["对话服务(Chatservice)"]
	if sectioned.Group != "对话服务(Chat Service)" {
		t.Errorf("section group = %q, want %q", sectioned.Group, "对话服务(Chat Service)")
	}
	ungrouped := byName["DeepSeek V4 Pro API服务(API Service)"]
	if ungrouped.Group != "" {
		t.Errorf("component without a section has group %q, want empty", ungrouped.Group)
	}

	wantOrder := []string{
		"DeepSeek V4 Pro API服务(API Service)",
		"DeepSeek V4.1 Flash API服务(API Service)",
		"对话服务(Chatservice)",
		"上传文件服务(File Upload Service)",
		"搜索服务(Search Service)",
	}
	for i, want := range wantOrder {
		if rep.Components[i].Name != want {
			t.Errorf("position %d = %q, want %q", i, rep.Components[i].Name, want)
		}
	}
	for i := 1; i < len(rep.Components); i++ {
		if rep.Components[i].Position <= rep.Components[i-1].Position {
			t.Errorf("positions are not increasing: %d then %d",
				rep.Components[i-1].Position, rep.Components[i].Position)
		}
	}
}

func TestFlashDutyMapsActiveChange(t *testing.T) {
	body := rscPage(`a:{"initialPageConfig":{"page_id":1,"name":"X","sections":[],"components":[` +
		`{"component_id":"c1","name":"Web","order_id":1},` +
		`{"component_id":"c2","name":"API","order_id":2}]},` +
		`"active_changes":[{"change_id":99,"type":"incident","title":"API is down",` +
		`"status":"investigating","start_at_seconds":1700000000,` +
		`"affected_components":[{"component_id":"c2","name":"API","status":"full_outage"}]}]}`)

	srv, adapter := serveFlashDuty(t, body)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got := map[string]adapters.Health{}
	for _, c := range rep.Components {
		got[c.SourceID] = c.Status
	}
	if got["c1"] != adapters.HealthOK {
		t.Errorf("c1 = %q, want ok (not mentioned in any active change)", got["c1"])
	}
	if got["c2"] != adapters.HealthMajor {
		t.Errorf("c2 = %q, want major", got["c2"])
	}
	if rep.Global != adapters.HealthMajor {
		t.Errorf("Global = %q, want major", rep.Global)
	}
	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents = %d, want 1", len(rep.Incidents))
	}
	inc := rep.Incidents[0]
	if inc.SourceID != "99" || inc.Name != "API is down" || inc.Status != "investigating" {
		t.Errorf("unexpected incident: %+v", inc)
	}
	if inc.Impact != string(adapters.HealthMajor) {
		t.Errorf("impact = %q, want %q", inc.Impact, adapters.HealthMajor)
	}
	if inc.StartedAt.IsZero() {
		t.Error("incident start time is zero")
	}
	if inc.ResolvedAt != nil {
		t.Error("active incident should not be resolved")
	}
}

func TestFlashDutyUnknownVocabularyIsNotOk(t *testing.T) {
	body := rscPage(`a:{"initialPageConfig":{"page_id":1,"name":"X","sections":[],"components":[` +
		`{"component_id":"c1","name":"Web","order_id":1},` +
		`{"component_id":"c2","name":"API","order_id":2}]},` +
		`"active_changes":[{"change_id":1,"type":"incident","title":"?",` +
		`"status":"investigating","start_at_seconds":1700000000,` +
		`"affected_components":[{"component_id":"c2","name":"API","status":"brand_new_word"}]}]}`)

	srv, adapter := serveFlashDuty(t, body)
	rep, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for _, c := range rep.Components {
		if c.SourceID == "c2" && c.Status != adapters.HealthUnknown {
			t.Errorf("c2 = %q, want unknown", c.Status)
		}
	}
	if rep.Global == adapters.HealthOK {
		t.Error("global must not be ok while a component status is unknown")
	}
	if rep.Global != adapters.HealthUnknown {
		t.Errorf("Global = %q, want unknown", rep.Global)
	}
}

func TestFlashDutyRejectsPlainHTML(t *testing.T) {
	srv, adapter := serveFlashDuty(t, []byte("<html><body>not a status page</body></html>"))
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL}); err == nil {
		t.Fatal("expected an error when there is no rsc payload")
	}
}

func TestFlashDutyRejectsMissingActiveChanges(t *testing.T) {
	body := rscPage(`a:{"initialPageConfig":{"page_id":1,"name":"X","sections":[],"components":[` +
		`{"component_id":"c1","name":"Web","order_id":1}]}}`)

	srv, adapter := serveFlashDuty(t, body)
	_, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error when active_changes is missing")
	}
	if !strings.Contains(err.Error(), "active_changes") {
		t.Errorf("error = %q, want it to mention active_changes", err)
	}
}

func TestFlashDutyRejectsPageWithoutComponents(t *testing.T) {
	body := rscPage(`a:{"initialPageConfig":{"page_id":1,"name":"X","sections":[],"components":[]},` +
		`"active_changes":[]}`)

	srv, adapter := serveFlashDuty(t, body)
	if _, err := adapter.Fetch(context.Background(), adapters.Target{URL: srv.URL}); err == nil {
		t.Fatal("expected an error when the page reports no components")
	}
}

func TestUnescapeJSString(t *testing.T) {
	cases := map[string]string{
		`plain`:           "plain",
		`a\"b`:            `a"b`,
		`a\\b`:            `a\b`,
		`line\nbreak`:     "line\nbreak",
		`tab\there`:       "tab\there",
		`slash\/here`:     "slash/here",
		`unicode\u4e2d`:   "unicode中",
		`deepseek \u4e0a`: "deepseek 上",
	}
	for in, want := range cases {
		got, err := unescapeJSString(in)
		if err != nil {
			t.Errorf("unescape(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := unescapeJSString(`bad\q`); err == nil {
		t.Error("expected an error for an unsupported escape")
	}
	if _, err := unescapeJSString(`trailing\`); err == nil {
		t.Error("expected an error for a trailing backslash")
	}
}

func TestExtractJSONValue(t *testing.T) {
	raw := `x:"k":{"a":1,"nested":{"b":[1,2,{"c":"}"}]},"s":"}"}y:"arr":[1,{"z":2}]`

	got, ok := extractJSONValue(raw, `"k":`)
	if !ok {
		t.Fatal("object not found")
	}
	if want := `{"a":1,"nested":{"b":[1,2,{"c":"}"}]},"s":"}"}`; got != want {
		t.Errorf("object = %q, want %q", got, want)
	}

	got, ok = extractJSONValue(raw, `"arr":`)
	if !ok {
		t.Fatal("array not found")
	}
	if want := `[1,{"z":2}]`; got != want {
		t.Errorf("array = %q, want %q", got, want)
	}

	if _, ok := extractJSONValue(raw, `"missing":`); ok {
		t.Error("expected not found for a missing key")
	}
}
