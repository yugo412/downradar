package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// A signed webhook is saved with the secret it will sign with, and the answer to
// that save is the only place the secret can be read. A page that comes back says
// one is set and never what it is.
func TestASigningSecretIsHandedBackInTheSaveThatMadeIt(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)
	path := "/api/v1/vendors/" + slug + "/alerts"

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	// A signed webhook has nothing to sign with yet. Saving it as one would leave
	// calls that say they are signed and are not, so it is refused.
	rec := call(t, router, http.MethodPost, path,
		`{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/downradar","auth":"standard"}}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a signed webhook with no secret = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	signed := `{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/downradar",` +
		`"method":"POST","auth":"standard","generateSecret":true}}`
	rec = call(t, router, http.MethodPost, path, signed, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("making a secret = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var made struct {
		Status string `json:"status"`
		Secret string `json:"webhookSecret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(made.Secret, "whsec_") || len(made.Secret) <= len("whsec_") {
		t.Fatalf("webhookSecret = %q, want a whsec_ secret in the answer that made it", made.Secret)
	}

	// What the page reads afterwards says a secret is set, and not what it is.
	state := readBell(t, router, slug, cookie)
	if state.Webhook == nil || state.Webhook.Auth != store.WebhookAuthStandard || !state.Webhook.Signed {
		t.Fatalf("webhook = %+v, want a signed one", state.Webhook)
	}
	read := call(t, router, http.MethodGet, path, "", cookie)
	if strings.Contains(read.Body.String(), made.Secret) {
		t.Errorf("the answer carries the secret:\n%s", read.Body.String())
	}

	// Making another one is a rotation, and it is handed back the same way.
	rec = call(t, router, http.MethodPost, path, signed, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rotating the secret = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var rotated struct {
		Secret string `json:"webhookSecret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rotated.Secret == "" || rotated.Secret == made.Secret {
		t.Errorf("webhookSecret = %q, want a new secret", rotated.Secret)
	}

	// A save that does not ask for one keeps the secret that is there.
	rec = call(t, router, http.MethodPost, path,
		`{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/downradar","auth":"standard"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving again = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if again := readBell(t, router, slug, cookie); again.Webhook == nil || !again.Webhook.Signed {
		t.Errorf("webhook = %+v, want the secret kept by a save that said nothing about it", again.Webhook)
	}
}

// Custom headers are still a way to authenticate, and a webhook that uses them
// carries no secret. A page that predates the choice keeps them too.
func TestACustomWebhookAsksForNoSecret(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)
	path := "/api/v1/vendors/" + slug + "/alerts"

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	// A secret for a webhook that is not signing makes no sense.
	rec := call(t, router, http.MethodPost, path,
		`{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/x","auth":"custom","generateSecret":true}}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a secret asked for by a custom webhook = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, path,
		`{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/x","auth":"custom","generateSecret":false}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a custom webhook = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	state := readBell(t, router, slug, cookie)
	if state.Webhook == nil || state.Webhook.Auth != store.WebhookAuthCustom || state.Webhook.Signed {
		t.Errorf("webhook = %+v, want custom with no secret", state.Webhook)
	}

	// The bell, and any page that predates the choice, says nothing about auth:
	// that saves the webhook as it was rather than changing how it calls.
	other := alertVendor(t, st)
	otherPath := "/api/v1/vendors/" + other + "/alerts"
	rec = call(t, router, http.MethodPost, otherPath,
		`{"wholeService":true,"webhook":{"url":"https://hooks.example.invalid/old-page"}}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a save with no choice = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	inOther := readBell(t, router, other, cookie)
	if inOther.Webhook == nil || inOther.Webhook.Auth != store.WebhookAuthCustom {
		t.Errorf("webhook = %+v, want the custom headers it was saved with", inOther.Webhook)
	}
}

// A test call that says it is signed needs the secret to sign with, and a
// webhook that has not been given one is told so rather than called.
func TestATestCallNeedsASecretBeforeItCanClaimOne(t *testing.T) {
	st, router := testRouter(t)
	slug := alertVendor(t, st)

	subscriber := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, subscriber)

	rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+slug+"/alerts/test",
		`{"url":"https://hooks.example.invalid/downradar","auth":"standard"}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a signed try with no secret = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "signing secret") {
		t.Errorf("answer = %s, want it to say what is missing", rec.Body.String())
	}
}
