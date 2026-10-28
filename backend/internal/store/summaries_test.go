package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestGetVendorHidesTheSummaryWhenDisabled(t *testing.T) {
	st := integrationStoreWith(t, Options{DisableSummaries: true})
	ctx := context.Background()

	slug := uniqueName("summary hidden")
	id := alertService(t, st, slug)
	const text = "The API is degraded while an incident is open."
	if err := st.SaveGeneratedSummary(ctx, id, text, "gpt-6-luna", "hash", time.Now().UTC()); err != nil {
		t.Fatalf("SaveGeneratedSummary: %v", err)
	}

	detail, err := st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor: %v", err)
	}
	if detail.GeneratedSummary != nil {
		t.Errorf("disabled summaries still exposed %+v", detail.GeneratedSummary)
	}

	// The row stays put, so enabling summaries again reads the same sentence.
	st.summariesEnabled = true
	detail, err = st.GetVendor(ctx, slug, Public())
	if err != nil {
		t.Fatalf("GetVendor with summaries enabled: %v", err)
	}
	if detail.GeneratedSummary == nil || detail.GeneratedSummary.Text != text {
		t.Errorf("re-enabled summaries did not reuse the stored sentence: %+v", detail.GeneratedSummary)
	}
}

func TestPendingAlertsHideTheSummaryWhenDisabled(t *testing.T) {
	st := integrationStoreWith(t, Options{DisableSummaries: true})
	ctx := context.Background()

	slug := uniqueName("summary alert")
	id := alertService(t, st, slug)
	token := fmt.Sprintf("token-summary-%d", time.Now().UnixNano())
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("summary@example.com", token, WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SaveGeneratedSummary(ctx, id, "The API is degraded.", "gpt-6-luna", "hash", time.Now().UTC()); err != nil {
		t.Fatalf("SaveGeneratedSummary: %v", err)
	}
	queueDeliveryFor(t, st, token, nil)

	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.ServiceID != id {
			continue
		}
		if alert.GeneratedSummary != "" || alert.SummaryGeneratedAt != nil {
			t.Errorf("disabled summaries still reached the alert: %+v", alert)
		}
		return
	}
	t.Fatal("the queued alert was not found")
}
