package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// A signed webhook's secret is made by the save that asks for it and read in that
// save's answer. Every answer after that only says one is set: the row keeps what
// it needs to sign with, and nothing hands it back.
func TestASigningSecretIsMadeOnceAndReadAgainOnlyByRotation(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("webhook secret"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(ctx, st, email) })
	user, err := st.CreateUser(ctx, email, "Signed Up Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	hook := AlertWebhook{
		URL:    "https://hooks.example.invalid/downradar",
		Method: "POST",
		Auth:   WebhookAuthStandard,
	}
	secret, err := st.SubscribeAlertWithSecret(ctx, id, &user.ID, AlertChange{
		Webhook: &hook, WebhookToken: "token-" + email, Targets: WholeVendor,
	})
	if err != nil {
		t.Fatalf("SubscribeAlertWithSecret: %v", err)
	}
	if !strings.HasPrefix(secret, "whsec_") || len(secret) <= len("whsec_") {
		t.Fatalf("secret = %q, want a whsec_ secret", secret)
	}
	if _, err := WebhookSigningKey(secret); err != nil {
		t.Errorf("the secret cannot be read back as a key: %v", err)
	}

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Webhook == nil {
		t.Fatal("the webhook was not saved")
	}
	if view.Webhook.Auth != WebhookAuthStandard || !view.Webhook.Signed {
		t.Errorf("webhook = %+v, want a signed one", view.Webhook)
	}
	if view.Webhook.Signing.Secret != secret {
		t.Error("the saved secret is not the one that was handed back")
	}

	// What a page is told never carries the secret itself.
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("the answer carries the secret:\n%s", encoded)
	}

	// A save that says nothing about the secret keeps it, which is what makes
	// saving the page safe after the secret has been made.
	again := AlertWebhook{URL: hook.URL, Method: "PUT", Auth: WebhookAuthStandard}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &again, WebhookToken: "unused", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	kept, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if kept.Webhook == nil || kept.Webhook.Signing.Secret != secret {
		t.Errorf("a save that said nothing about the secret dropped it")
	}
	if kept.Webhook.Method != "PUT" {
		t.Errorf("method = %q, want the save to write the rest of the webhook", kept.Webhook.Method)
	}

	// Making another one is a rotation: the new secret is what signs from now on,
	// and the old one keeps going out for a day so a receiver can move over.
	rotated, err := st.SubscribeAlertWithSecret(ctx, id, &user.ID, AlertChange{
		Webhook: &again, WebhookToken: "unused", Targets: WholeVendor,
	})
	if err != nil {
		t.Fatalf("the rotation: %v", err)
	}
	if rotated == secret {
		t.Error("the rotation handed back the same secret")
	}
	after, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if after.Webhook.Signing.Secret != rotated {
		t.Error("the rotated secret is not the one that was handed back")
	}
	if after.Webhook.Signing.Previous != secret {
		t.Error("the retired secret is not kept for a receiver that has not moved yet")
	}
	grace := time.Until(after.Webhook.Signing.PreviousUntil)
	if grace <= 0 || grace > WebhookSecretGrace {
		t.Errorf("the retired secret has %v left, want up to %v", grace, WebhookSecretGrace)
	}
}

// A webhook that says its calls are signed cannot be saved without a secret, and
// a secret is only for one that is signing.
func TestASignedWebhookNeedsASecret(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("webhook needs secret"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(ctx, st, email) })
	user, err := st.CreateUser(ctx, email, "Signed Up Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	signed := AlertWebhook{
		URL:    "https://hooks.example.invalid/downradar",
		Method: "POST",
		Auth:   WebhookAuthStandard,
	}
	err = st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &signed, WebhookToken: "token-" + email, Targets: WholeVendor,
	})
	if !errors.Is(err, ErrWebhookSecretMissing) {
		t.Errorf("err = %v, want a signed webhook without a secret to be refused", err)
	}

	custom := AlertWebhook{
		URL:    "https://hooks.example.invalid/downradar",
		Method: "POST",
		Auth:   WebhookAuthCustom,
	}
	_, err = st.SubscribeAlertWithSecret(ctx, id, &user.ID, AlertChange{
		Webhook: &custom, WebhookToken: "token-" + email, Targets: WholeVendor,
	})
	if !errors.Is(err, ErrWebhookSecretUnused) {
		t.Errorf("err = %v, want a secret asked for on a custom webhook to be refused", err)
	}
}

// A webhook saved before there were two ways to authenticate keeps sending the
// headers it was sending: its settings have no auth, and it reads as custom.
func TestAWebhookSavedBeforeTheChoiceIsCustom(t *testing.T) {
	old, err := parseWebhookSettings([]byte(`{"method":"PATCH","headers":[{"name":"X-Key","value":"abc"}]}`), "https://hooks.example.invalid/x")
	if err != nil {
		t.Fatalf("parseWebhookSettings: %v", err)
	}
	if old.Auth != WebhookAuthCustom {
		t.Errorf("auth = %q, want custom", old.Auth)
	}
	if old.Signed {
		t.Error("a webhook with no secret says it is signed")
	}
	if old.Method != "PATCH" || len(old.Headers) != 1 {
		t.Errorf("webhook = %+v, want the settings kept as they were", old)
	}

	// And what the new code writes reads back the same.
	want := AlertWebhook{
		URL:    "https://hooks.example.invalid/x",
		Method: "POST",
		Auth:   WebhookAuthStandard,
		Signing: WebhookSigning{
			Secret:        "whsec_c2VjcmV0",
			CreatedAt:     time.Now().UTC().Truncate(time.Millisecond),
			Previous:      "whsec_b2xk",
			PreviousUntil: time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond),
		},
	}
	raw, err := encodeWebhook(want)
	if err != nil {
		t.Fatalf("encodeWebhook: %v", err)
	}
	got, err := parseWebhookSettings(raw, want.URL)
	if err != nil {
		t.Fatalf("parseWebhookSettings: %v", err)
	}
	if got.Auth != WebhookAuthStandard || !got.Signed {
		t.Errorf("webhook = %+v, want the choice and the secret kept", got)
	}
	if !got.Signing.CreatedAt.Equal(want.Signing.CreatedAt) ||
		!got.Signing.PreviousUntil.Equal(want.Signing.PreviousUntil) ||
		got.Signing.Previous != want.Signing.Previous {
		t.Errorf("signing = %+v, want %+v", got.Signing, want.Signing)
	}
}
