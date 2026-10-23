package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"downradar.info/backend/internal/adapters"
)

// alertService is a vendor the public can see, which is the only kind an alert
// can be subscribed to.
func alertService(t *testing.T, st *Store, slug string) int64 {
	t.Helper()

	id := testService(t, st, slug)
	if _, err := st.pool.Exec(context.Background(), `
		update services set status = 'published', visibility = 'public', active = true where id = $1`, id); err != nil {
		t.Fatalf("make service public: %v", err)
	}
	return id
}

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()

	var n int
	if err := st.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// queuedFor counts the deliveries waiting for one service. Other tests share the
// database, so a count of the whole table would depend on what they left behind.
func queuedFor(t *testing.T, st *Store, serviceID int64) int {
	t.Helper()

	return countRows(t, st, `
		select count(*)
		from alert_deliveries d
		where d.service_id = $1 and d.sent_at is null`, serviceID)
}

func reportWith(status adapters.Health) adapters.Report {
	rep := testReport("")
	rep.Global = status
	// Old enough that a queued delivery has passed its settle window.
	rep.FetchedAt = time.Now().UTC().Add(-time.Hour)
	return rep
}

// componentIDFor is the row a target points at for one of a vendor's own
// identifiers.
func componentIDFor(t *testing.T, st *Store, serviceID int64, sourceID string) int64 {
	t.Helper()

	var id int64
	if err := st.pool.QueryRow(context.Background(),
		`select id from components where service_id = $1 and source_id = $2`,
		serviceID, sourceID).Scan(&id); err != nil {
		t.Fatalf("read component %s: %v", sourceID, err)
	}
	return id
}

// queuedForToken counts what is waiting for one address's alert, which is how a
// test tells which subscription a change woke.
func queuedForToken(t *testing.T, st *Store, token string) int {
	t.Helper()

	return countRows(t, st, `
		select count(*)
		from alert_deliveries d
		join alert_channels c on c.id = d.channel_id
		where c.token = $1 and d.sent_at is null`, token)
}

// queueDeliveryFor writes the delivery a change of status would have written for
// one alert's channel. A nil component is the vendor's own status, which is what
// the delivery rows use to mean it.
func queueDeliveryFor(t *testing.T, st *Store, token string, componentID *int64) {
	t.Helper()

	if _, err := st.pool.Exec(context.Background(), `
		insert into alert_deliveries (channel_id, service_id, component_id, from_status, to_status, changed_at)
		select c.id, a.service_id, $2, 'ok', 'major', now()
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where c.token = $1`, token, componentID); err != nil {
		t.Fatalf("queue a delivery: %v", err)
	}
}

func TestSubscribeAlertKeepsOneRowPerAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert once"))

	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("ann@example.com", "token-one", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// The address and the vendor are the subscription, so asking twice is not
	// two subscriptions, and the token already in an inbox keeps working.
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("ann@example.com", "token-two", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert again: %v", err)
	}

	if n := countRows(t, st, `select count(*) from alert_subscriptions a`+
		` join alert_targets t on t.subscription_id = a.id where t.service_id = $1`, id); n != 1 {
		t.Errorf("subscriptions = %d, want 1", n)
	}

	view, err := st.AlertByToken(ctx, "token-one")
	if err != nil {
		t.Fatalf("AlertByToken: %v", err)
	}
	if view.UnsubscribedAt != nil {
		t.Error("a fresh subscription reads as unsubscribed")
	}
}

func TestUnsubscribeAlertStopsItAndAllowsItAgain(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert unsubscribe"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("bob@example.com", "token-bob", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if err := st.UnsubscribeAlert(ctx, "token-bob"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	view, err := st.AlertByToken(ctx, "token-bob")
	if err != nil {
		t.Fatalf("AlertByToken: %v", err)
	}
	if view.UnsubscribedAt == nil {
		t.Error("the subscription does not read as unsubscribed")
	}

	// Subscribing again is what a visitor does when they change their mind. The
	// row is reused, so the old link still resolves.
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("bob@example.com", "token-ignored", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert after unsubscribing: %v", err)
	}
	view, err = st.AlertByToken(ctx, "token-bob")
	if err != nil {
		t.Fatalf("AlertByToken after subscribing again: %v", err)
	}
	if view.UnsubscribedAt != nil {
		t.Error("subscribing again did not clear the unsubscribed timestamp")
	}

	if err := st.UnsubscribeAlert(ctx, "not-a-token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unsubscribing an unknown token = %v, want ErrNotFound", err)
	}
}

func TestUnsubscribeAlertDropsWhatWasStillWaiting(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert drop"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("cara@example.com", "token-cara", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries d`+
		` where d.sent_at is null and d.service_id = $1`, id); n != 1 {
		t.Fatalf("pending deliveries = %d, want 1", n)
	}

	if err := st.UnsubscribeAlert(ctx, "token-cara"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries d`+
		` where d.service_id = $1`, id); n != 0 {
		t.Errorf("deliveries = %d, want the waiting one dropped", n)
	}
}

func TestApplyReportQueuesOneAlertPerChange(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert change"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("dave@example.com", "token-dave", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// The first status a service is seen in is a baseline, not a change.
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("deliveries after the first poll = %d, want none", n)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	// Polling the same status again is not another change.
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("third poll: %v", err)
	}

	// The queue is shared with whatever else the database holds, so the question
	// is what this change wrote rather than how many rows the table has: the
	// running stack may be delivering something of its own at the same time.
	if n := queuedFor(t, st, id); n != 1 {
		t.Fatalf("waiting for this service = %d, want one alert", n)
	}

	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}

	var alert *PendingAlert
	for i := range pending {
		if pending[i].ServiceID == id {
			alert = &pending[i]
		}
	}
	if alert == nil {
		t.Fatalf("the alert for this service is not queued")
	}
	if alert.Email != "dave@example.com" || alert.Token != "token-dave" {
		t.Errorf("alert goes to %q with token %q", alert.Email, alert.Token)
	}
	if alert.VendorSlug == "" || alert.VendorName == "" {
		t.Errorf("alert does not name the vendor: %+v", alert)
	}
	if alert.FromStatus != string(adapters.HealthOK) || alert.ToStatus != string(adapters.HealthMajor) {
		t.Errorf("alert says %s -> %s, want ok -> major", alert.FromStatus, alert.ToStatus)
	}

	// Once it is marked sent it leaves the queue, and a failure is counted so a
	// dead address cannot be retried for ever.
	if err := st.SentAlert(ctx, alert.ID, alert.ToStatus, alert.ChangedAt); err != nil {
		t.Fatalf("SentAlert: %v", err)
	}
	pending, err = st.PendingAlerts(ctx, 3, 10)
	if err != nil {
		t.Fatalf("PendingAlerts after sending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending after sending = %d, want none", len(pending))
	}
}

func TestAChangeWaitsForItsSettleWindow(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert settle"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("jane@example.com", "token-jane", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	rep := testReport("")
	rep.FetchedAt = time.Now().UTC()
	rep.Global = adapters.HealthOK
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	rep.Global = adapters.HealthMajor
	rep.FetchedAt = time.Now().UTC()
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	var due time.Time
	if err := st.pool.QueryRow(ctx, `
		select deliver_after from alert_deliveries
		where service_id = $1 and sent_at is null`, id).Scan(&due); err != nil {
		t.Fatalf("read deliver_after: %v", err)
	}
	if wait := time.Until(due); wait < 4*time.Minute || wait > 6*time.Minute {
		t.Errorf("delivery waits %v, want about the settle window", wait)
	}

	// A change that has not settled is not handed to the sender yet.
	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.ServiceID == id {
			t.Errorf("an unsettled change is already queued: %+v", alert)
		}
	}

	if _, err := st.pool.Exec(ctx, `
		update alert_deliveries set deliver_after = now() - interval '1 second'
		where service_id = $1 and sent_at is null`, id); err != nil {
		t.Fatalf("release the delivery: %v", err)
	}
	pending, err = st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts after the wait: %v", err)
	}
	for _, alert := range pending {
		if alert.ServiceID == id {
			if alert.FromStatus != string(adapters.HealthOK) || alert.ToStatus != string(adapters.HealthMajor) {
				t.Errorf("alert says %s -> %s, want ok -> major", alert.FromStatus, alert.ToStatus)
			}
			return
		}
	}
	t.Fatal("a settled change is not queued")
}

func TestAChangeBurstBecomesOneMessage(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert burst"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("kate@example.com", "token-kate", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	rep := testReport("")
	rep.FetchedAt = time.Now().UTC()
	rep.Global = adapters.HealthOK
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	for _, status := range []adapters.Health{adapters.HealthMajor, adapters.HealthDegraded} {
		rep.Global = status
		rep.FetchedAt = time.Now().UTC()
		if _, err := st.ApplyReport(ctx, id, rep); err != nil {
			t.Fatalf("poll to %s: %v", status, err)
		}
	}

	if n := queuedFor(t, st, id); n != 1 {
		t.Fatalf("waiting after a burst = %d, want one message", n)
	}
	var from, to string
	if err := st.pool.QueryRow(ctx, `
		select from_status, to_status from alert_deliveries
		where service_id = $1 and sent_at is null`, id).Scan(&from, &to); err != nil {
		t.Fatalf("read the burst delivery: %v", err)
	}
	if from != string(adapters.HealthOK) || to != string(adapters.HealthDegraded) {
		t.Errorf("burst says %s -> %s, want ok -> degraded", from, to)
	}

	// Returning to where the burst started leaves nothing to announce.
	rep.Global = adapters.HealthOK
	rep.FetchedAt = time.Now().UTC()
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("poll back to ok: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("waiting after the burst returned = %d, want none", n)
	}

	// Holding the delivery does not drop any of the history: every transition is
	// still an event on the vendor.
	if n := countRows(t, st, `
		select count(*) from status_events
		where service_id = $1 and component_id is null`, id); n != 4 {
		t.Errorf("vendor events = %d, want the baseline and every transition", n)
	}
}

func TestAStaleSendLeavesTheNewerChangePending(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert stale send"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("mia@example.com", "token-mia", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	rep := testReport("")
	rep.FetchedAt = time.Now().UTC()
	rep.Global = adapters.HealthOK
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	rep.Global = adapters.HealthMajor
	rep.FetchedAt = time.Now().UTC()
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	var alertID int64
	var sentAt *time.Time
	var changedAt time.Time
	if err := st.pool.QueryRow(ctx, `
		select id, changed_at from alert_deliveries
		where service_id = $1 and sent_at is null`, id).Scan(&alertID, &changedAt); err != nil {
		t.Fatalf("read the first change: %v", err)
	}

	rep.Global = adapters.HealthDegraded
	rep.FetchedAt = time.Now().UTC()
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("third poll: %v", err)
	}

	// The sender had the first change in hand; its answer must not retire the
	// change that replaced it.
	if err := st.SentAlert(ctx, alertID, string(adapters.HealthMajor), changedAt); err != nil {
		t.Fatalf("SentAlert: %v", err)
	}
	if err := st.pool.QueryRow(ctx, `
		select sent_at from alert_deliveries where id = $1`, alertID).Scan(&sentAt); err != nil {
		t.Fatalf("read sent_at: %v", err)
	}
	if sentAt != nil {
		t.Error("a stale send marked the newer change as delivered")
	}
	if n := queuedFor(t, st, id); n != 1 {
		t.Errorf("waiting after a stale send = %d, want the newer change", n)
	}
}

func TestAComponentBurstBecomesOneMessage(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert component burst"))
	report := func(status adapters.Health) adapters.Report {
		rep := testReport("")
		rep.FetchedAt = time.Now().UTC()
		rep.Components[0].Status = status
		return rep
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	componentIDs, err := st.ComponentIDs(ctx, id, []string{"c1"})
	if err != nil {
		t.Fatalf("ComponentIDs: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("leo@example.com", "token-leo",
		AlertTargets{Components: componentIDs})); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	for _, status := range []adapters.Health{adapters.HealthDegraded, adapters.HealthMajor} {
		if _, err := st.ApplyReport(ctx, id, report(status)); err != nil {
			t.Fatalf("poll to %s: %v", status, err)
		}
	}

	if n := queuedFor(t, st, id); n != 1 {
		t.Fatalf("waiting after the component burst = %d, want one message", n)
	}
	var from, to string
	if err := st.pool.QueryRow(ctx, `
		select from_status, to_status from alert_deliveries
		where service_id = $1 and component_id is not null and sent_at is null`, id).Scan(&from, &to); err != nil {
		t.Fatalf("read the component burst: %v", err)
	}
	if from != string(adapters.HealthOK) || to != string(adapters.HealthMajor) {
		t.Errorf("component burst says %s -> %s, want ok -> major", from, to)
	}

	// Returning to where it started leaves nothing to announce.
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK)); err != nil {
		t.Fatalf("poll back to ok: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("waiting after the component returned = %d, want none", n)
	}
}

func TestPendingAlertsLeavesUnsubscribedAddressesAlone(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert quiet"))
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("erin@example.com", "token-erin", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthDegraded)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	// A change that happens after the address has gone is not an alert: the row
	// stays, but nothing is waiting for it.
	if _, err := st.pool.Exec(ctx, `
		update alert_channels set unsubscribed_at = now()
		where subscription_id in (
			select subscription_id from alert_targets where service_id = $1)`, id); err != nil {
		t.Fatalf("unsubscribe by hand: %v", err)
	}

	pending, err := st.PendingAlerts(ctx, 3, 10)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	for _, alert := range pending {
		if alert.Email == "erin@example.com" {
			t.Errorf("an unsubscribed address is still queued: %+v", alert)
		}
	}
}

func TestPublicServiceHidesWhatThePublicCannotSee(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := uniqueName("alert private")
	id := alertService(t, st, slug)

	if _, name, err := st.PublicService(ctx, slug); err != nil || name == "" {
		t.Fatalf("PublicService on a public vendor = %q, %v", name, err)
	}

	// A private service has no vendor page, so an alert about it would link
	// somewhere its reader cannot go.
	ownerEmail := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, ownerEmail) })
	owner, err := st.CreateUser(ctx, ownerEmail, "Owner Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`update services set owner_id = $2, visibility = 'private' where id = $1`, id, owner.ID); err != nil {
		t.Fatalf("make service private: %v", err)
	}
	if _, _, err := st.PublicService(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("PublicService on a private vendor = %v, want ErrNotFound", err)
	}
}

func TestAlertElsewhere(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	firstSlug := uniqueName("alert first")
	secondSlug := uniqueName("alert second")
	first := alertService(t, st, firstSlug)
	second := alertService(t, st, secondSlug)

	if err := st.SubscribeAlert(ctx, first, nil, EmailOnly("gina@example.com", "token-gina", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// The vendor it is already subscribed to is not "elsewhere".
	if _, err := st.AlertElsewhere(ctx, "gina@example.com", first); !errors.Is(err, ErrNotFound) {
		t.Errorf("AlertElsewhere on the vendor it is subscribed to = %v, want ErrNotFound", err)
	}

	ref, err := st.AlertElsewhere(ctx, "gina@example.com", second)
	if err != nil {
		t.Fatalf("AlertElsewhere: %v", err)
	}
	if ref.Slug != firstSlug {
		t.Errorf("ref = %+v, want the vendor the address is subscribed to (%s)", ref, firstSlug)
	}
	if ref.Name == "" {
		t.Error("the vendor is not named, which leaves the answer to be guessed at")
	}

	if _, err := st.AlertElsewhere(ctx, "someone-else@example.com", second); !errors.Is(err, ErrNotFound) {
		t.Errorf("AlertElsewhere for an address with no alert = %v, want ErrNotFound", err)
	}

	// Unsubscribing gives the address its one alert back.
	if err := st.UnsubscribeAlert(ctx, "token-gina"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if _, err := st.AlertElsewhere(ctx, "gina@example.com", second); !errors.Is(err, ErrNotFound) {
		t.Errorf("AlertElsewhere after unsubscribing = %v, want ErrNotFound", err)
	}

	// A vendor that has been taken out of the catalogue is not polled, so an
	// alert on it cannot arrive and must not hold the visitor's place.
	if err := st.SubscribeAlert(ctx, second, nil, EmailOnly("gina@example.com", "token-gina-two", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `update services set active = false where id = $1`, second); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := st.AlertElsewhere(ctx, "gina@example.com", first); !errors.Is(err, ErrNotFound) {
		t.Errorf("AlertElsewhere with only a gone vendor = %v, want ErrNotFound", err)
	}
}

func TestUserAlertsListsWhatTheAccountHas(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	firstSlug := uniqueName("alert list one")
	secondSlug := uniqueName("alert list two")
	first := alertService(t, st, firstSlug)
	second := alertService(t, st, secondSlug)

	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, first, &user.ID, EmailOnly("work@example.org", "token-list-one", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SubscribeAlert(ctx, second, &user.ID, EmailOnly("home@example.net", "token-list-two", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// Somebody else's alert is not in this account's list.
	if err := st.SubscribeAlert(ctx, first, nil, EmailOnly("guest@example.com", "token-list-guest", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	alerts, err := st.UserAlerts(ctx, user.ID)
	if err != nil {
		t.Fatalf("UserAlerts: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("alerts = %d, want the account's two", len(alerts))
	}
	// Newest first, and each one says what it is doing.
	if alerts[0].VendorSlug != secondSlug || alerts[0].State() != AlertActive {
		t.Errorf("first = %+v (%s), want %s and active", alerts[0], alerts[0].State(), secondSlug)
	}
	if alerts[0].VendorStatus != "unknown" {
		t.Errorf("vendor status = %q, want unknown for a vendor nothing has polled", alerts[0].VendorStatus)
	}
	if alerts[1].Email != "work@example.org" {
		t.Errorf("email = %q, want the address the alert goes to", alerts[1].Email)
	}
	if alerts[0].ID == 0 {
		t.Error("an alert in the list has no id to act on")
	}
}

func TestSetUserAlertStatePausesAndResumes(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert pause"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("work@example.org", "token-pause", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	row := func(t *testing.T) AlertView {
		t.Helper()
		view, err := st.UserAlert(ctx, user.ID, id)
		if err != nil {
			t.Fatalf("UserAlert: %v", err)
		}
		return *view
	}

	// Pausing drops what was waiting: a resume should not deliver last week's
	// news, and a paused address gets no email in between either.
	if err := st.SetUserAlertState(ctx, user.ID, row(t).ID, AlertPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := row(t).State(); got != AlertPaused {
		t.Errorf("state = %q, want paused", got)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("pending = %d, want the waiting alert dropped on pause", n)
	}

	// A status change while paused is not queued for that address either.
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("pending = %d, want nothing queued while paused", n)
	}

	if err := st.SetUserAlertState(ctx, user.ID, row(t).ID, AlertActive); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := row(t).State(); got != AlertActive {
		t.Errorf("state after resuming = %q, want active", got)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("fourth poll: %v", err)
	}
	if n := queuedFor(t, st, id); n != 1 {
		t.Errorf("pending = %d, want an alert once resumed", n)
	}

	// Ending it supersedes the pause, and somebody else's row is not theirs to
	// change.
	if err := st.SetUserAlertState(ctx, user.ID, row(t).ID, AlertUnsubscribed); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	view, err := st.UserAlert(ctx, user.ID, id)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert after ending = %v (%+v), want ErrNotFound", err, view)
	}

	other, err := st.CreateUser(ctx, uniqueTestEmail(), "Somebody Else", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t.Cleanup(func() { dropTestUser(context.Background(), st, other.Email) })
	if err := st.SetUserAlertState(ctx, other.ID, 1, AlertPaused); !errors.Is(err, ErrNotFound) {
		t.Errorf("pausing a row that is not theirs = %v, want ErrNotFound", err)
	}
}

func TestDeleteUserAlertRemovesTheRow(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert delete"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("work@example.org", "token-delete", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}

	if err := st.DeleteUserAlert(ctx, user.ID, view.ID); err != nil {
		t.Fatalf("DeleteUserAlert: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_subscriptions where id = $1`, view.ID); n != 0 {
		t.Errorf("rows left = %d, want the row gone", n)
	}
	if err := st.DeleteUserAlert(ctx, user.ID, view.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it twice = %v, want ErrNotFound", err)
	}
}

func TestUserAlertFollowsTheAccountNotTheAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert user view"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// An alert made while signed out belongs to the address: the person looking
	// at the page may not be the one who made it, so the bell must not claim it.
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("guest@example.com", "token-guest", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert for a guest's alert = %v, want ErrNotFound", err)
	}

	// One made while signed in does, even when the address is not the account's.
	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("work@example.org", "token-work", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Email != "work@example.org" {
		t.Errorf("email = %q, want the address the alert goes to", view.Email)
	}

	// Stopping it from the page takes the account's alert and leaves the guest's.
	stopped, err := st.UnsubscribeUserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UnsubscribeUserAlert: %v", err)
	}
	if stopped != 1 {
		t.Errorf("stopped = %d, want 1", stopped)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert after stopping = %v, want ErrNotFound", err)
	}

	var guestStill bool
	if err := st.pool.QueryRow(ctx,
		`select unsubscribed_at is null from alert_channels where token = 'token-guest'`).Scan(&guestStill); err != nil {
		t.Fatalf("read the guest's alert: %v", err)
	}
	if !guestStill {
		t.Error("stopping the account's alerts also stopped somebody else's")
	}
}

func TestUnsubscribeUserAlertDropsWhatWasStillWaiting(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert user drop"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("work@example.org", "token-work", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := queuedFor(t, st, id); n != 1 {
		t.Fatalf("pending = %d, want one alert waiting", n)
	}

	if _, err := st.UnsubscribeUserAlert(ctx, user.ID, id); err != nil {
		t.Fatalf("UnsubscribeUserAlert: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("pending = %d, want the waiting alert dropped", n)
	}
}

func TestSubscribeAlertRecordsTheSignedInUser(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert user"))

	// A visitor with no account is the normal case: the row stands on its own.
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("frank@example.com", "token-frank", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	var userID *int64
	if err := st.pool.QueryRow(ctx,
		`select a.user_id from alert_subscriptions a`+
			` join alert_channels c on c.subscription_id = a.id where c.token = 'token-frank'`).Scan(&userID); err != nil {
		t.Fatalf("read user_id: %v", err)
	}
	if userID != nil {
		t.Errorf("user_id = %d, want null for an anonymous visitor", *userID)
	}

	// Subscribing again while signed out must not clear a link that was made
	// while signed in.
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("frank@example.com", "token-frank", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert signed in: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("frank@example.com", "token-frank", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert signed out again: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`select a.user_id from alert_subscriptions a`+
			` join alert_channels c on c.subscription_id = a.id where c.token = 'token-frank'`).Scan(&userID); err != nil {
		t.Fatalf("read user_id: %v", err)
	}
	if userID == nil || *userID != user.ID {
		t.Errorf("user_id = %v, want the id it was given", userID)
	}
}

// A subscription can arrive more than one way, and the link in a message stops
// the channel that message came from: the subscription is still active while any
// of its channels can deliver, and reads as unsubscribed only when none can.
func TestEachChannelStopsOnItsOwn(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert channels"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("work@example.org", "token-mail", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// The vendor page only makes email channels. A webhook is the shape a second
	// one takes, so it is written by hand: the point of the change is that the
	// tables hold it without another one.
	var subscriptionID int64
	if err := st.pool.QueryRow(ctx,
		`select subscription_id from alert_channels where token = 'token-mail'`).Scan(&subscriptionID); err != nil {
		t.Fatalf("read the subscription: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		insert into alert_channels (subscription_id, kind, address, token)
		values ($1, 'webhook', 'https://hooks.example.com/downradar', 'token-hook')`,
		subscriptionID); err != nil {
		t.Fatalf("add a webhook channel: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := queuedFor(t, st, id); n != 2 {
		t.Fatalf("pending = %d, want one per channel", n)
	}

	// The unsubscribe link stops the channel it came from and leaves the other
	// one alone, so the person is still reachable the other way.
	if err := st.UnsubscribeAlert(ctx, "token-mail"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if n := queuedFor(t, st, id); n != 1 {
		t.Errorf("pending = %d, want only the webhook's alert left", n)
	}

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if got := view.State(); got != AlertActive {
		t.Errorf("state = %q, want active while a channel can still deliver", got)
	}

	// With the last channel ended the subscription reads as unsubscribed, and the
	// screen still lists it so it can be started again.
	if err := st.UnsubscribeAlert(ctx, "token-hook"); err != nil {
		t.Fatalf("UnsubscribeAlert the webhook: %v", err)
	}
	if n := queuedFor(t, st, id); n != 0 {
		t.Errorf("pending = %d, want nothing left waiting", n)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert with every channel ended = %v, want ErrNotFound", err)
	}

	alerts, err := st.UserAlerts(ctx, user.ID)
	if err != nil {
		t.Fatalf("UserAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want the ended subscription still listed", len(alerts))
	}
	if got := alerts[0].State(); got != AlertUnsubscribed {
		t.Errorf("state = %q, want unsubscribed once every channel has ended", got)
	}
}

// A subscription that watches one component is not woken by the vendor's own
// status: what it asked about is that component, and the change it waits for is
// the component's own.
func TestAComponentTargetWaitsForTheComponent(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert component"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A poll records the vendor's component, which is what a target can point at.
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	componentID := componentIDFor(t, st, id, "c1")

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly("component@example.org",
		"token-component", AlertTargets{Components: []int64{componentID}})); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// It watches the component and not the vendor: a vendor target written beside
	// it would be a second thing the address is told about.
	if n := countRows(t, st, `
		select count(*) from alert_targets t
		join alert_channels c on c.subscription_id = t.subscription_id
		where c.token = 'token-component' and t.component_id is null`); n != 0 {
		t.Error("a component target was written with a vendor target beside it")
	}

	// The bell reads back what is watched, so it can open with that box ticked.
	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.WholeService {
		t.Error("the bell was told the vendor's own status is watched")
	}
	if len(view.ComponentIDs) != 1 || view.ComponentIDs[0] != "c1" {
		t.Errorf("components = %v, want c1", view.ComponentIDs)
	}

	// The vendor going down is not the change it asked about.
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := queuedForToken(t, st, "token-component"); n != 0 {
		t.Errorf("pending = %d, want nothing queued for a component target", n)
	}
}

// The change a component subscription waits for is the component's own, and the
// vendor's own status is a different one: a vendor reported down for something
// nobody asked about says nothing about the component they did.
func TestAComponentChangeWakesOnlyItsComponentTarget(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert component change"))
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	componentID := componentIDFor(t, st, id, "c1")

	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("watching-service@example.org",
		"token-whole", WholeVendor)); err != nil {
		t.Fatalf("subscribe to the vendor: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, EmailOnly("watching-component@example.org",
		"token-part", AlertTargets{Components: []int64{componentID}})); err != nil {
		t.Fatalf("subscribe to the component: %v", err)
	}

	// The vendor moves while the component still reports fine: only the address
	// watching the vendor hears about it.
	rep := reportWith(adapters.HealthMajor)
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := queuedForToken(t, st, "token-whole"); n != 1 {
		t.Errorf("the vendor target queued %d, want 1", n)
	}
	if n := queuedForToken(t, st, "token-part"); n != 0 {
		t.Errorf("the component target queued %d, want nothing", n)
	}

	// Then the component moves while the vendor's own status does not: the same
	// rule the other way round.
	rep = reportWith(adapters.HealthMajor)
	rep.Components[0].Status = adapters.HealthDegraded
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	if n := queuedForToken(t, st, "token-part"); n != 1 {
		t.Errorf("the component target queued %d, want 1", n)
	}
	if n := queuedForToken(t, st, "token-whole"); n != 1 {
		t.Errorf("the vendor target queued %d, want the one it already had and no more", n)
	}
}

// Asking again replaces what is watched and keeps the email token and anything
// still waiting for a watched target.
func TestAskingAgainReplacesWhatIsWatched(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert change mind"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Two components, so one of them can be dropped further down.
	rep := reportWith(adapters.HealthOK)
	rep.Components = append(rep.Components, adapters.ComponentStatus{
		SourceID: "c2", Name: "API", Position: 2, Status: adapters.HealthOK,
	})
	if _, err := st.ApplyReport(ctx, id, rep); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	first, second := componentIDFor(t, st, id, "c1"), componentIDFor(t, st, id, "c2")

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly(email, "token-mind", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly(email, "token-unused",
		AlertTargets{Components: []int64{first, second}})); err != nil {
		t.Fatalf("change of mind: %v", err)
	}
	targets := `
		select count(*) from alert_targets t
		join alert_channels c on c.subscription_id = t.subscription_id
		where c.token = 'token-mind'`
	if n := countRows(t, st, targets+` and t.component_id is not null`); n != 2 {
		t.Errorf("component targets = %d, want the two that were asked for", n)
	}
	if n := countRows(t, st, targets+` and t.component_id is null`); n != 0 {
		t.Error("the vendor target outlived the change of mind")
	}
	if n := countRows(t, st,
		`select count(*) from alert_channels where token = 'token-mind'`); n != 1 {
		t.Error("the channel did not keep the token its previous email carries")
	}

	// What is waiting for a component is kept when that component is still
	// watched, and goes with the component when it is dropped.
	queueDeliveryFor(t, st, "token-mind", &first)
	queueDeliveryFor(t, st, "token-mind", &second)
	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly(email, "token-unused",
		AlertTargets{Components: []int64{first}})); err != nil {
		t.Fatalf("drop one component: %v", err)
	}
	if n := queuedForToken(t, st, "token-mind"); n != 1 {
		t.Errorf("pending = %d, want the one for the component still watched", n)
	}

	// And the whole vendor can be picked again, which takes the components with
	// it: one thing watched, not three.
	if err := st.SubscribeAlert(ctx, id, &user.ID, EmailOnly(email, "token-unused", WholeVendor)); err != nil {
		t.Fatalf("change back: %v", err)
	}
	if n := countRows(t, st, targets); n != 1 {
		t.Errorf("targets = %d, want only the vendor once it is picked again", n)
	}
}

// Purging a vendor takes the alerts that were about it and leaves the ones on
// other vendors: an alert is an alert about one vendor, so there is nothing left
// behind holding an address and a token for a page that is gone.
func TestPurgingAVendorTakesTheAlertThatOnlyWatchedIt(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	goneSlug := uniqueName("alert purge gone")
	keptSlug := uniqueName("alert purge kept")
	gone := alertService(t, st, goneSlug)
	kept := alertService(t, st, keptSlug)

	// One alert on the vendor that is about to go, watching only one of its
	// components, and one on a vendor that stays.
	if _, err := st.ApplyReport(ctx, gone, testReport("")); err != nil {
		t.Fatalf("poll the vendor: %v", err)
	}
	goneComponent := componentIDFor(t, st, gone, "c1")
	if err := st.SubscribeAlert(ctx, gone, nil, EmailOnly("anna@example.com", "token-purge-gone",
		AlertTargets{Components: []int64{goneComponent}})); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SubscribeAlert(ctx, kept, nil, EmailOnly("bella@example.com", "token-purge-kept", WholeVendor)); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if err := st.PurgeService(ctx, goneSlug); err != nil {
		t.Fatalf("PurgeService: %v", err)
	}

	if n := countRows(t, st,
		`select count(*) from alert_channels where token = $1`, "token-purge-gone"); n != 0 {
		t.Error("the alert on the purged vendor is still there")
	}
	if n := countRows(t, st,
		`select count(*) from alert_subscriptions where service_id = $1`, gone); n != 0 {
		t.Error("the subscription on the purged vendor is still there")
	}
	if n := countRows(t, st,
		`select count(*) from alert_channels where token = $1`, "token-purge-kept"); n != 1 {
		t.Errorf("the alert on the other vendor = %d, want it kept", n)
	}
}

// A webhook sits beside the address, keeps its own token, and can be stopped
// without ending the alert.
func TestAWebhookIsASecondWayForOneAlert(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert webhook"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Signed In Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	address := "hooks@example.org"
	hook := AlertWebhook{
		URL:     "https://hooks.example.invalid/downradar",
		Method:  "POST",
		Headers: []AlertField{{Name: "X-Source", Value: "downradar"}},
	}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email: &address, EmailToken: "token-mail",
		Webhook: &hook, WebhookToken: "token-hook",
		Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// One alert, two ways, each with a token of its own.
	if n := countRows(t, st, `
		select count(*) from alert_subscriptions a
		where a.user_id = $1
		  and exists (select 1 from alert_channels c
		              where c.subscription_id = a.id and c.token = 'token-mail')
		  and exists (select 1 from alert_channels c
		              where c.subscription_id = a.id and c.token = 'token-hook')`,
		user.ID); n != 1 {
		t.Errorf("subscriptions carrying both ways = %d, want one", n)
	}

	// The page reads both ways back, and the header list is what was written and
	// nothing else: nothing is added to a webhook behind the subscriber's back.
	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Email != address {
		t.Errorf("email = %q, want the address the alert goes to", view.Email)
	}
	if view.Webhook == nil {
		t.Fatal("the page cannot read back the webhook it saved")
	}
	if view.Webhook.URL != hook.URL || view.Webhook.Method != "POST" {
		t.Errorf("webhook = %+v, want the call that was saved", view.Webhook)
	}
	if len(view.Webhook.Headers) != 1 {
		t.Fatalf("headers = %+v, want only the one that was written", view.Webhook.Headers)
	}
	if got := view.Webhook.Headers[0]; got.Name != "X-Source" || got.Value != "downradar" {
		t.Errorf("header = %+v, want it kept as it was written", got)
	}

	// A save about the webhook alone leaves the address where it is, and moving
	// where it calls keeps the row and its token.
	moved := hook
	moved.URL = "https://hooks.example.invalid/moved"
	moved.Headers = append(moved.Headers, AlertField{Name: "X-Env", Value: "prod"})
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &moved, WebhookToken: "token-other", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("moving the webhook: %v", err)
	}
	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Email != address {
		t.Errorf("email = %q, want it left alone by a save about the webhook", view.Email)
	}
	if view.Webhook.URL != moved.URL || len(view.Webhook.Headers) != 2 {
		t.Errorf("webhook = %+v, want what the second save asked for", view.Webhook)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where kind = 'webhook' and token = 'token-hook'`); n != 1 {
		t.Errorf("webhook channels = %d, want the one that keeps its token", n)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where kind = 'webhook'
		  and subscription_id = (select subscription_id from alert_channels
		                         where token = 'token-hook')`); n != 1 {
		t.Errorf("webhook channels on the subscription = %d, want one", n)
	}

	// Stopping the calls ends that way and leaves the emails. The row keeps its
	// token, so the same URL can be started again later.
	off := AlertWebhook{}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &off, WebhookToken: "token-unused", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("stopping the webhook: %v", err)
	}
	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Webhook != nil {
		t.Errorf("webhook = %+v, want the calls stopped", view.Webhook)
	}
	if view.Email != address {
		t.Errorf("email = %q, want the emails still going", view.Email)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-hook' and unsubscribed_at is not null`); n != 1 {
		t.Error("a stopped webhook lost its row, so its token cannot be reached again")
	}

	// Stopping the emails too leaves nothing that can arrive, which is no alert
	// to read back while the screen still lists the row to start again.
	none := ""
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email: &none, Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("stopping the emails: %v", err)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert with every way stopped = %v, want ErrNotFound", err)
	}
	alerts, err := st.UserAlerts(ctx, user.ID)
	if err != nil {
		t.Fatalf("UserAlerts: %v", err)
	}
	if len(alerts) != 1 || alerts[0].State() != AlertUnsubscribed {
		t.Errorf("rows = %+v, want the stopped alert still listed", alerts)
	}

	// A save with another address changes the address on the same alert rather
	// than leaving a second one beside it, and it is the email channel's own
	// token that comes back to life.
	movedAddress := "hooks2@example.org"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email: &movedAddress, EmailToken: "token-unused", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("moving the address: %v", err)
	}
	if n := countRows(t, st, `
		select count(*) from alert_subscriptions where user_id = $1`, user.ID); n != 1 {
		t.Errorf("subscriptions = %d, want the one alert this account has here", n)
	}
	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Email != movedAddress {
		t.Errorf("email = %q, want the new address", view.Email)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-mail' and address = $1 and unsubscribed_at is null`,
		movedAddress); n != 1 {
		t.Errorf("the email channel = %d, want the one that keeps its token", n)
	}
}

// A change writes one delivery per channel, and what a webhook delivery carries
// is the call the subscriber described, so the sender has it in hand without
// asking for anything else.
func TestAPendingWebhookCarriesItsCall(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert webhook pending"))
	token := uniqueName("token-hook-pending")
	hook := AlertWebhook{
		URL:    "https://hooks.example.invalid/pending",
		Method: "PUT",
	}
	if err := st.SubscribeAlert(ctx, id, nil, AlertChange{
		Webhook: &hook, WebhookToken: token, Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// The delivery is dropped before the test ends, so nothing is left for the
	// running development stack to call.
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	queueDeliveryFor(t, st, token, nil)

	pending, err := st.PendingAlerts(ctx, 3, 100)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}

	found := false
	for _, alert := range pending {
		if alert.Token != token {
			continue
		}
		found = true
		if alert.Kind != ChannelWebhook {
			t.Errorf("kind = %q, want a webhook", alert.Kind)
		}
		if alert.Email != "" {
			t.Errorf("email = %q, want nothing where a webhook carries its URL", alert.Email)
		}
		if alert.Webhook.URL != hook.URL || alert.Webhook.Method != "PUT" {
			t.Errorf("webhook = %+v, want the call that was saved", alert.Webhook)
		}
		// The header list is the subscriber's: nothing is added to it, and a call
		// carries exactly what was written.
		if len(alert.Webhook.Headers) != 0 {
			t.Errorf("headers = %+v, want nothing the subscriber did not write", alert.Webhook.Headers)
		}
	}
	if !found {
		t.Fatal("no delivery was waiting for the webhook")
	}
}

// A webhook hears about every component, while an email only hears about what it
// was told to report on.
func TestAWebhookHearsAboutEveryComponent(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert every component"))

	// The email reports on the vendor's own status, so the component below is
	// nothing it asked about.
	email := "gina@example.com"
	hook := AlertWebhook{URL: "https://hooks.example.invalid/every", Method: "POST"}
	if err := st.SubscribeAlert(ctx, id, nil, AlertChange{
		Email: &email, EmailToken: "token-every-mail",
		Webhook: &hook, WebhookToken: "token-every-hook",
		Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() {
		_ = st.UnsubscribeAlert(context.Background(), "token-every-mail")
		_ = st.UnsubscribeAlert(context.Background(), "token-every-hook")
	})

	// whatIsWaiting counts one way's deliveries for one thing that changed, so a
	// test can tell what a change woke rather than only how many rows there are.
	whatIsWaiting := func(token string, componentID *int64) int {
		t.Helper()
		return countRows(t, st, `
			select count(*)
			from alert_deliveries d
			join alert_channels c on c.id = d.channel_id
			where c.token = $1 and d.component_id is not distinct from $2
			  and d.sent_at is null`, token, componentID)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	componentID := componentIDFor(t, st, id, "c1")
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := whatIsWaiting("token-every-mail", nil); n != 1 {
		t.Errorf("emails after the vendor moved = %d, want one", n)
	}
	if n := whatIsWaiting("token-every-hook", nil); n != 1 {
		t.Errorf("calls after the vendor moved = %d, want one", n)
	}

	// The component moves without the vendor moving at all.
	moved := reportWith(adapters.HealthMajor)
	moved.Components = []adapters.ComponentStatus{
		{SourceID: "c1", Name: "Web", Position: 1, Status: adapters.HealthDegraded},
	}
	if _, err := st.ApplyReport(ctx, id, moved); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	if n := whatIsWaiting("token-every-mail", &componentID); n != 0 {
		t.Errorf("emails about a component nobody picked = %d, want none", n)
	}
	if n := whatIsWaiting("token-every-hook", &componentID); n != 1 {
		t.Errorf("calls about the component = %d, want one", n)
	}
}

// Pausing a way is a channel going quiet without being taken away: what it was
// told stays where it is, nothing more goes out by it, and the other way carries
// on as it did.
func TestPausingOneWayLeavesTheOther(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert pause way"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Pausing Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	address := "pause@example.org"
	hook := AlertWebhook{URL: "https://hooks.example.invalid/pause", Method: "POST"}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email: &address, EmailToken: "token-pause-mail",
		Webhook: &hook, WebhookToken: "token-pause-hook",
		Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// Pausing one way drops what was waiting for it and leaves the other's.
	queueDeliveryFor(t, st, "token-pause-mail", nil)
	queueDeliveryFor(t, st, "token-pause-hook", nil)
	if err := st.SetChannelPaused(ctx, user.ID, id, ChannelEmail, true); err != nil {
		t.Fatalf("SetChannelPaused: %v", err)
	}
	if n := queuedForToken(t, st, "token-pause-mail"); n != 0 {
		t.Errorf("emails waiting = %d, want the paused way to have dropped its own", n)
	}
	if n := queuedForToken(t, st, "token-pause-hook"); n != 1 {
		t.Errorf("calls waiting = %d, want the other way left alone", n)
	}

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if !view.EmailPaused || view.WebhookPaused {
		t.Errorf("ways are quiet = email %v, webhook %v, want only the email",
			view.EmailPaused, view.WebhookPaused)
	}
	if view.Email != address || view.Webhook == nil {
		t.Error("pausing a way took away what it was told")
	}
	if view.State() != AlertActive {
		t.Errorf("state = %q, want an alert with a way still live", view.State())
	}

	// A save leaves a paused way paused: the switch is what starts it again, not
	// the save.
	moved := hook
	moved.URL = "https://hooks.example.invalid/pause-moved"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Webhook: &moved, WebhookToken: "token-unused", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("saving again: %v", err)
	}
	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if !view.EmailPaused {
		t.Error("a save started a paused way again")
	}

	// Starting it again is the switch the other way, and it goes out as before.
	if err := st.SetChannelPaused(ctx, user.ID, id, ChannelEmail, false); err != nil {
		t.Fatalf("SetChannelPaused: %v", err)
	}
	queueDeliveryFor(t, st, "token-pause-mail", nil)
	if n := queuedForToken(t, st, "token-pause-mail"); n != 1 {
		t.Errorf("emails waiting after starting again = %d, want one", n)
	}

	// Every way quiet is what the account screen calls a paused alert.
	for _, kind := range []string{ChannelEmail, ChannelWebhook} {
		if err := st.SetChannelPaused(ctx, user.ID, id, kind, true); err != nil {
			t.Fatalf("SetChannelPaused(%s): %v", kind, err)
		}
	}
	alerts, err := st.UserAlerts(ctx, user.ID)
	if err != nil {
		t.Fatalf("UserAlerts: %v", err)
	}
	found := false
	for _, alert := range alerts {
		if alert.ID == view.ID && alert.State() == AlertPaused && alert.EmailPaused && alert.WebhookPaused {
			found = true
		}
	}
	if !found {
		t.Errorf("the account screen does not show the alert as paused: %+v", alerts)
	}

	// A way the alert does not have cannot be paused.
	other := alertService(t, st, uniqueName("alert pause other"))
	if err := st.SetChannelPaused(ctx, user.ID, other, ChannelEmail, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("pausing a way that is not there = %v, want ErrNotFound", err)
	}
}

// The log of an alert's calls is the last few and no more, so an alert called
// every day does not grow a table nobody reads.
func TestWebhookCallsAreKept(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert call log"))
	email := uniqueTestEmail()
	t.Cleanup(func() { dropTestUser(context.Background(), st, email) })
	user, err := st.CreateUser(ctx, email, "Called Person", "subscriber", "h")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	written := WebhookCallsKept + 3
	for i := 0; i < written; i++ {
		status := 200
		if err := st.LogWebhookCall(ctx, &user.ID, id, nil, WebhookCall{
			Kind:     CallTest,
			URL:      "https://hooks.example.invalid/log",
			Method:   "POST",
			Status:   &status,
			Response: fmt.Sprintf("answer %d", i),
		}); err != nil {
			t.Fatalf("LogWebhookCall: %v", err)
		}
	}

	kept := countRows(t, st, `
		select count(*) from alert_webhook_calls where user_id = $1 and service_id = $2`,
		user.ID, id)
	if kept != WebhookCallsKept {
		t.Errorf("calls kept = %d, want the newest %d", kept, WebhookCallsKept)
	}

	calls, err := st.WebhookCalls(ctx, user.ID, id, 10)
	if err != nil {
		t.Fatalf("WebhookCalls: %v", err)
	}
	if len(calls) != 10 {
		t.Fatalf("calls read = %d, want the ten that were asked for", len(calls))
	}
	if want := fmt.Sprintf("answer %d", written-1); calls[0].Response != want {
		t.Errorf("newest call = %q, want %q", calls[0].Response, want)
	}
	if calls[0].Kind != CallTest || calls[0].Status == nil || *calls[0].Status != 200 {
		t.Errorf("newest call = %+v, want what was written", calls[0])
	}

	// A subscription with nobody behind it has nobody to show a log to, so
	// nothing is written for it.
	before := countRows(t, st, `select count(*) from alert_webhook_calls where service_id = $1`, id)
	if err := st.LogWebhookCall(ctx, nil, id, nil, WebhookCall{
		Kind: CallDelivery, URL: "https://hooks.example.invalid/log", Method: "POST",
	}); err != nil {
		t.Fatalf("LogWebhookCall with no owner: %v", err)
	}
	if after := countRows(t, st, `select count(*) from alert_webhook_calls where service_id = $1`, id); after != before {
		t.Errorf("calls after a write with no owner = %d, want %d", after, before)
	}
}

// A try from the page is built from a snapshot alone, so the snapshot has to name
// the vendor rather than only say what state it is in.
func TestTheSnapshotNamesTheService(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	slug := uniqueName("alert snapshot")
	id := alertService(t, st, slug)
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	snapshot, err := st.ServiceSnapshot(ctx, id)
	if err != nil {
		t.Fatalf("ServiceSnapshot: %v", err)
	}
	if snapshot.Name != slug || snapshot.Slug != slug {
		t.Errorf("snapshot names %q and %q, want the vendor it is about", snapshot.Name, snapshot.Slug)
	}
	if snapshot.Status != string(adapters.HealthOK) {
		t.Errorf("snapshot status = %q, want ok", snapshot.Status)
	}
	if len(snapshot.Components) != 1 {
		t.Errorf("snapshot components = %d, want the one the vendor reports", len(snapshot.Components))
	}
}

// A chat is a third way for one alert, and it belongs to an account: it is saved
// from the page beside the other ways, and clearing it stops the messages the way
// an empty address stops the emails.
func TestAChatIsAThirdWayForOneAlert(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("alert chat"))

	email := "hana@example.com"
	chat := &AlertTelegram{ChatID: "-1001234567890", BotToken: "999:bot-secret"}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email:         &email,
		EmailToken:    "token-chat-mail",
		Telegram:      chat,
		TelegramToken: "token-chat-telegram",
		Targets:       WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() {
		_ = st.UnsubscribeAlert(context.Background(), "token-chat-mail")
		_ = st.UnsubscribeAlert(context.Background(), "token-chat-telegram")
	})

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Telegram != chat.ChatID {
		t.Errorf("chat = %q, want %q", view.Telegram, chat.ChatID)
	}
	if view.TelegramBot != chat.BotToken {
		t.Errorf("bot = %q, want the one the chat was saved with", view.TelegramBot)
	}
	if view.Email != email {
		t.Errorf("email = %q, want %q", view.Email, email)
	}
	if view.TelegramPaused {
		t.Error("a chat that was just saved reads as paused")
	}

	// Saving the same chat again is the same channel rather than a second one, so
	// the token a message already carries still stops it.
	moved := &AlertTelegram{ChatID: "-1009999999999", BotToken: chat.BotToken}
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram: moved, TelegramToken: "token-chat-other",
	}); err != nil {
		t.Fatalf("SubscribeAlert again: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_channels
		where subscription_id = (select id from alert_subscriptions where user_id = $1 and service_id = $2)
		  and kind = 'telegram'`, user.ID, id); n != 1 {
		t.Errorf("chat channels = %d, want one", n)
	}
	if n := countRows(t, st,
		`select count(*) from alert_channels where token = $1`, "token-chat-telegram"); n != 1 {
		t.Error("the chat lost the token a message already carries")
	}
	again, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after moving the chat: %v", err)
	}
	if again.Telegram != moved.ChatID {
		t.Errorf("chat = %q, want the one that was named", again.Telegram)
	}
	if again.TelegramBot != chat.BotToken {
		t.Errorf("bot = %q, want the one already there", again.TelegramBot)
	}

	// An empty chat is somebody stopping the messages: the way ends and what was
	// waiting for it goes, while the address beside it carries on.
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram: &AlertTelegram{},
	}); err != nil {
		t.Fatalf("stopping the chat: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_channels
		where token = $1 and unsubscribed_at is not null`, "token-chat-telegram"); n != 1 {
		t.Error("the chat way was not stopped")
	}
	after, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after stopping the chat: %v", err)
	}
	if after.Telegram != "" {
		t.Errorf("chat = %q, want the way gone", after.Telegram)
	}
	if after.Email != email {
		t.Errorf("email = %q, want the address left alone", after.Email)
	}
}

// A chat is told what an address is told: the things its subscriber picked, and
// nothing else the vendor reports. A webhook is the one that hears about the
// whole vendor.
func TestAChatIsToldWhatTheAddressIsTold(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("alert chat component"))

	report := func(first, second adapters.Health) adapters.Report {
		return adapters.Report{
			Global:    adapters.HealthOK,
			FetchedAt: time.Now().UTC(),
			Components: []adapters.ComponentStatus{
				{SourceID: "c1", Name: "Web", Position: 1, Status: first},
				{SourceID: "c2", Name: "API", Position: 2, Status: second},
			},
		}
	}
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthOK, adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	picked, err := st.ComponentIDs(ctx, id, []string{"c1"})
	if err != nil {
		t.Fatalf("ComponentIDs: %v", err)
	}

	email := "ivan@example.com"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email:         &email,
		EmailToken:    "token-chat-mail-c",
		Telegram:      &AlertTelegram{ChatID: "42", BotToken: "999:bot-secret"},
		TelegramToken: "token-chat-telegram-c",
		Targets:       AlertTargets{Components: picked},
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() {
		_ = st.UnsubscribeAlert(context.Background(), "token-chat-mail-c")
		_ = st.UnsubscribeAlert(context.Background(), "token-chat-telegram-c")
	})

	waiting := func(token string, componentID *int64) int {
		t.Helper()
		return countRows(t, st, `
			select count(*)
			from alert_deliveries d
			join alert_channels c on c.id = d.channel_id
			where c.token = $1 and d.component_id is not distinct from $2
			  and d.sent_at is null`, token, componentID)
	}

	// The component the subscriber picked moves, so both ways hear about it.
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthDegraded, adapters.HealthOK)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	first := componentIDFor(t, st, id, "c1")
	for _, token := range []string{"token-chat-mail-c", "token-chat-telegram-c"} {
		if n := waiting(token, &first); n != 1 {
			t.Errorf("%s after the picked component moved = %d, want one", token, n)
		}
	}

	// The other component moves, which neither of them asked about.
	if _, err := st.ApplyReport(ctx, id, report(adapters.HealthDegraded, adapters.HealthMajor)); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	second := componentIDFor(t, st, id, "c2")
	for _, token := range []string{"token-chat-mail-c", "token-chat-telegram-c"} {
		if n := waiting(token, &second); n != 0 {
			t.Errorf("%s about a component nobody picked = %d, want none", token, n)
		}
	}
}

// A chat with no bot could never be sent to, so the first save has to name one
// rather than leave a way that looks set up and is not. The bot is kept with the
// channel, and read back for a message tried from the page.
func TestAChatNeedsABotToBeSentWith(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	slug := uniqueName("alert chat bot")
	id := alertService(t, st, slug)
	token := "token-bot-" + slug
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram: &AlertTelegram{ChatID: "42"},
		Targets:  WholeVendor,
	})
	if !errors.Is(err, ErrNoBotToken) {
		t.Fatalf("saving a chat with no bot = %v, want ErrNoBotToken", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram:      &AlertTelegram{ChatID: "42", BotToken: "999:bot-secret"},
		TelegramToken: token,
		Targets:       WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	bot, err := st.TelegramBot(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("TelegramBot: %v", err)
	}
	if bot != "999:bot-secret" {
		t.Errorf("bot = %q, want the one the chat was saved with", bot)
	}
}

// The link in a message can quieten the way it came from without taking it away,
// and the token is the whole of what proves it belongs to whoever opened it.
func TestALinkCanPauseTheWayItCameFrom(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	slug := uniqueName("alert pause link")
	id := alertService(t, st, slug)
	token := "token-pause-" + slug
	t.Cleanup(func() { _ = st.UnsubscribeAlert(context.Background(), token) })

	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Telegram:      &AlertTelegram{ChatID: "42", BotToken: "999:bot-secret"},
		TelegramToken: token,
		Targets:       WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	waiting := func() int {
		t.Helper()
		return countRows(t, st, `
			select count(*)
			from alert_deliveries d
			join alert_channels c on c.id = d.channel_id
			where c.token = $1 and d.sent_at is null`, token)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := waiting(); n != 1 {
		t.Fatalf("waiting for the chat = %d, want one", n)
	}

	if err := st.SetChannelPausedByToken(ctx, token, true); err != nil {
		t.Fatalf("pause by its link: %v", err)
	}
	view, err := st.AlertByToken(ctx, token)
	if err != nil {
		t.Fatalf("AlertByToken: %v", err)
	}
	if view.PausedAt == nil {
		t.Error("the way the link came from is not paused")
	}
	if n := waiting(); n != 0 {
		t.Errorf("waiting after the pause = %d, want none", n)
	}

	// The same switch, the other way.
	if err := st.SetChannelPausedByToken(ctx, token, false); err != nil {
		t.Fatalf("start again by its link: %v", err)
	}
	view, err = st.AlertByToken(ctx, token)
	if err != nil {
		t.Fatalf("AlertByToken: %v", err)
	}
	if view.PausedAt != nil {
		t.Error("the way did not start again")
	}

	// A stopped way is not one a link can start, and a link that is not one has
	// nothing to pause at all.
	if err := st.UnsubscribeAlert(ctx, token); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if err := st.SetChannelPausedByToken(ctx, token, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("starting a stopped way = %v, want ErrNotFound", err)
	}
	if err := st.SetChannelPausedByToken(ctx, "not-a-token", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown link = %v, want ErrNotFound", err)
	}
}

// A Slack channel is a fourth way for one alert: it is saved beside an address,
// one URL names where it posts, and an empty one stops it. The URL is a secret,
// so it stays on the channel and the page is handed it to show masked.
func TestASlackChannelIsAFourthWayForOneAlert(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("alert slack"))

	email := "sari@example.com"
	const first = "https://hooks.slack.com/services/T000/B000/first"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email:      &email,
		EmailToken: "token-slack-mail",
		Slack:      &AlertSlack{URL: first},
		SlackToken: "token-slack-way",
		Targets:    WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() {
		_ = st.UnsubscribeAlert(context.Background(), "token-slack-mail")
		_ = st.UnsubscribeAlert(context.Background(), "token-slack-way")
	})

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Slack != first {
		t.Errorf("slack = %q, want %q", view.Slack, first)
	}
	if view.SlackPaused {
		t.Error("a channel that was just saved reads as paused")
	}
	if view.Email != email {
		t.Errorf("email = %q, want %q", view.Email, email)
	}

	// Saving a new URL for the way keeps it one channel, and keeps the token that
	// the link in a message already sent stops it by.
	const second = "https://hooks.slack.com/services/T000/B000/second"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Slack:      &AlertSlack{URL: second},
		SlackToken: "token-slack-other",
		Targets:    WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert again: %v", err)
	}

	if n := countRows(t, st, `
		select count(*) from alert_channels
		where subscription_id = (select id from alert_subscriptions where service_id = $1)
		  and kind = 'slack'`, id); n != 1 {
		t.Errorf("slack channels = %d, want one", n)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-slack-way' and address = $1`, second); n != 1 {
		t.Error("the saved channel was replaced with a new token instead of updated")
	}

	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after the change: %v", err)
	}
	if view.Slack != second {
		t.Errorf("slack = %q, want %q", view.Slack, second)
	}
	if view.Email != email {
		t.Errorf("saving the channel changed the email to %q", view.Email)
	}

	// A delivery waiting for the way carries the URL, because that is the whole of
	// where it posts.
	queueDeliveryFor(t, st, "token-slack-way", nil)
	pending, err := st.PendingAlerts(ctx, 3, 50)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	carried := false
	for _, alert := range pending {
		if alert.Kind == ChannelSlack && alert.SlackURL == second {
			carried = true
		}
	}
	if !carried {
		t.Error("a Slack delivery does not carry the webhook it posts to")
	}

	// An empty URL is the way being stopped, and the email beside it stays.
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Slack:   &AlertSlack{},
		Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert with no URL: %v", err)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-slack-way' and unsubscribed_at is not null`); n != 1 {
		t.Error("the stopped channel is not marked as stopped")
	}

	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after stopping: %v", err)
	}
	if view.Slack != "" {
		t.Errorf("slack = %q, want it empty once stopped", view.Slack)
	}
	if view.Email != email {
		t.Errorf("stopping the channel changed the email to %q", view.Email)
	}
}

// A Discord channel is the fifth way: one URL names where it posts, an empty one
// stops it, and the URL stays masked on the page.
func TestADiscordChannelIsAFifthWayForOneAlert(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	user := newTestUser(t, st, "subscriber")
	id := alertService(t, st, uniqueName("alert discord"))

	email := "sari@example.com"
	const first = "https://discord.com/api/webhooks/100/first"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Email:        &email,
		EmailToken:   "token-discord-mail",
		Discord:      &AlertDiscord{URL: first},
		DiscordToken: "token-discord-way",
		Targets:      WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	t.Cleanup(func() {
		_ = st.UnsubscribeAlert(context.Background(), "token-discord-mail")
		_ = st.UnsubscribeAlert(context.Background(), "token-discord-way")
	})

	view, err := st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert: %v", err)
	}
	if view.Discord != first {
		t.Errorf("discord = %q, want %q", view.Discord, first)
	}
	if view.DiscordPaused {
		t.Error("a channel that was just saved reads as paused")
	}
	if view.Email != email {
		t.Errorf("email = %q, want %q", view.Email, email)
	}

	// A new URL updates the same channel and keeps its token.
	const second = "https://discord.com/api/webhooks/100/second"
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Discord:      &AlertDiscord{URL: second},
		DiscordToken: "token-discord-other",
		Targets:      WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert again: %v", err)
	}

	if n := countRows(t, st, `
		select count(*) from alert_channels
		where subscription_id = (select id from alert_subscriptions where service_id = $1)
		  and kind = 'discord'`, id); n != 1 {
		t.Errorf("discord channels = %d, want one", n)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-discord-way' and address = $1`, second); n != 1 {
		t.Error("the saved channel was replaced with a new token instead of updated")
	}

	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after the change: %v", err)
	}
	if view.Discord != second {
		t.Errorf("discord = %q, want %q", view.Discord, second)
	}
	if view.Email != email {
		t.Errorf("saving the channel changed the email to %q", view.Email)
	}

	// A delivery carries the URL, which is the whole of where it posts.
	queueDeliveryFor(t, st, "token-discord-way", nil)
	pending, err := st.PendingAlerts(ctx, 3, 50)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	carried := false
	for _, alert := range pending {
		if alert.Kind == ChannelDiscord && alert.DiscordURL == second {
			carried = true
		}
	}
	if !carried {
		t.Error("a Discord delivery does not carry the webhook it posts to")
	}

	// An empty URL is the way being stopped, and the email beside it stays.
	if err := st.SubscribeAlert(ctx, id, &user.ID, AlertChange{
		Discord: &AlertDiscord{},
		Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert with no URL: %v", err)
	}
	if n := countRows(t, st, `
		select count(*) from alert_channels
		where token = 'token-discord-way' and unsubscribed_at is not null`); n != 1 {
		t.Error("the stopped channel is not marked as stopped")
	}

	view, err = st.UserAlert(ctx, user.ID, id)
	if err != nil {
		t.Fatalf("UserAlert after stopping: %v", err)
	}
	if view.Discord != "" {
		t.Errorf("discord = %q, want it empty once stopped", view.Discord)
	}
	if view.Email != email {
		t.Errorf("stopping the channel changed the email to %q", view.Email)
	}
}
