package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWebhookSettingsAreSealedAtRest(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("sealed webhook"))
	token := uniqueName("token-sealed-webhook")
	hook := AlertWebhook{
		URL:    "https://hooks.example.invalid/sealed",
		Method: "POST",
		Auth:   WebhookAuthCustom,
		Headers: []AlertField{
			{Name: "Authorization", Value: "Bearer top-secret"},
		},
	}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &hook, WebhookToken: token, Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	var raw []byte
	if err := st.pool.QueryRow(ctx, `
		select settings from alert_channels where token = $1`, token).Scan(&raw); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if bytes.Contains(raw, []byte("top-secret")) || bytes.Contains(raw, []byte("Authorization")) {
		t.Error("the settings column still carries the webhook secret")
	}
	var envelope sealedSettings
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if envelope.Version != 1 || envelope.Data == "" {
		t.Errorf("settings envelope = %+v, want a sealed value", envelope)
	}

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Webhook == nil || len(view.Webhook.Headers) != 1 ||
		view.Webhook.Headers[0].Value != "Bearer top-secret" {
		t.Errorf("webhook = %+v, want the header the subscriber saved", view.Webhook)
	}

	queueDeliveryFor(t, st, token, nil)
	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.Token != token {
			continue
		}
		if len(alert.Webhook.Headers) != 1 || alert.Webhook.Headers[0].Value != "Bearer top-secret" {
			t.Errorf("pending webhook = %+v, want the saved header", alert.Webhook)
		}
		return
	}
	t.Fatal("the webhook delivery is not pending")
}

func TestTelegramBotTokenIsSealedAtRest(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("sealed telegram"))
	token := uniqueName("token-sealed-telegram")
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram:      &AlertTelegram{ChatID: "42", BotToken: "999:bot-secret"},
		TelegramToken: token,
		Targets:       WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	var raw []byte
	if err := st.pool.QueryRow(ctx, `
		select settings from alert_channels where token = $1`, token).Scan(&raw); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if bytes.Contains(raw, []byte("bot-secret")) {
		t.Error("the settings column still carries the bot token")
	}

	bot, err := st.TelegramBot(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("TelegramBot: %v", err)
	}
	if bot != "999:bot-secret" {
		t.Errorf("bot = %q, want the saved token", bot)
	}

	queueDeliveryFor(t, st, token, nil)
	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.Token != token {
			continue
		}
		if alert.TelegramBot != "999:bot-secret" {
			t.Errorf("pending bot = %q, want the saved token", alert.TelegramBot)
		}
		return
	}
	t.Fatal("the chat delivery is not pending")
}

func TestLegacySettingsAreSealedOnStartup(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("legacy settings"))
	token := uniqueName("token-legacy-settings")
	hook := AlertWebhook{
		URL:    "https://hooks.example.invalid/legacy",
		Method: "POST",
		Auth:   WebhookAuthCustom,
	}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &hook, WebhookToken: token, Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	plain := []byte(`{"method":"PATCH","auth":"custom","headers":[{"name":"X-Key","value":"legacy-secret"}]}`)
	if _, err := st.pool.Exec(ctx, `
		update alert_channels set settings = $2 where token = $1`, token, plain); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	if err := st.sealLegacySettings(ctx, "token = $1", token); err != nil {
		t.Fatalf("sealLegacySettings: %v", err)
	}

	var raw []byte
	if err := st.pool.QueryRow(ctx, `
		select settings from alert_channels where token = $1`, token).Scan(&raw); err != nil {
		t.Fatalf("read sealed settings: %v", err)
	}
	if bytes.Contains(raw, []byte("legacy-secret")) {
		t.Error("the legacy settings are still plaintext")
	}
	var envelope sealedSettings
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode sealed settings: %v", err)
	}
	if envelope.Version != 1 || envelope.Data == "" {
		t.Errorf("settings envelope = %+v, want a sealed value", envelope)
	}

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Webhook == nil || len(view.Webhook.Headers) != 1 ||
		view.Webhook.Headers[0].Value != "legacy-secret" {
		t.Errorf("webhook = %+v, want the legacy header", view.Webhook)
	}
}

func TestAWrongAppKeyFailsLoudly(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("wrong app key"))
	token := uniqueName("token-wrong-key")
	hook := AlertWebhook{URL: "https://hooks.example.invalid/wrong", Method: "POST"}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &hook, WebhookToken: token, Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	other := integrationStoreWith(t, Options{AppKey: "another-app-key"})
	if err := other.sealLegacySettings(ctx, "token = $1", token); err == nil {
		t.Fatal("a store with another key opened the saved settings")
	}
}

func TestASaveWithoutAnAppKeyFails(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is not set, skipping the integration test")
	}

	ctx := context.Background()
	st, err := New(ctx, dsn, Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("no app key"))
	hook := AlertWebhook{URL: "https://hooks.example.invalid/no-key", Method: "POST"}
	err = st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &hook, WebhookToken: uniqueName("token-no-key"), Targets: WholeVendor,
	})
	if err == nil || !strings.Contains(err.Error(), "app key") {
		t.Fatalf("saving a webhook without a key = %v, want an app key error", err)
	}
}
