package store

import (
	"context"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

func TestApplyReportUsesTheComponentChangeTime(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	id := testService(t, st, "zz-test-component-time")

	sourceTime := time.Date(2026, 10, 28, 3, 11, 0, 0, time.UTC)
	rep := adapters.Report{
		Global:    adapters.HealthOK,
		FetchedAt: time.Now().UTC(),
		Components: []adapters.ComponentStatus{{
			SourceID: "c1", Name: "Web", Position: 1,
			Status: adapters.HealthOK, ChangedAt: sourceTime,
		}},
	}
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("ApplyReport: %v", err)
	}

	var componentAt time.Time
	err := st.pool.QueryRow(ctx, `
		select cs.changed_at
		from component_state cs
		join components c on c.id = cs.component_id
		where c.service_id = $1 and c.source_id = 'c1'`, id).Scan(&componentAt)
	if err != nil {
		t.Fatalf("read component_state: %v", err)
	}
	if !componentAt.Equal(sourceTime) {
		t.Errorf("component changed_at = %s, want %s", componentAt, sourceTime)
	}

	var eventAt, sourceChangedAt time.Time
	err = st.pool.QueryRow(ctx, `
		select changed_at, source_changed_at
		from status_events
		where service_id = $1 and component_id is not null
		order by id desc limit 1`, id).Scan(&eventAt, &sourceChangedAt)
	if err != nil {
		t.Fatalf("read status_event: %v", err)
	}
	if !eventAt.Equal(sourceTime) {
		t.Errorf("event changed_at = %s, want %s", eventAt, sourceTime)
	}
	if !sourceChangedAt.Equal(sourceTime) {
		t.Errorf("event source_changed_at = %s, want %s", sourceChangedAt, sourceTime)
	}
}
