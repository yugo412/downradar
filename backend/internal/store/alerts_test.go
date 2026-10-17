package store

import (
	"context"
	"errors"
	"testing"

	"downradar.info/backend/internal/adapters"
)

// alertService is a vendor the public can see, which is the only kind an alert
// can be subscribed to.
func alertService(t *testing.T, st *Store, slug string) int64 {
	t.Helper()

	id := testService(t, st, slug)
	if _, err := st.pool.Exec(context.Background(), `
		update services set status = 'published', owner_id = null, active = true where id = $1`, id); err != nil {
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
		join alert_targets t on t.id = d.target_id
		where t.service_id = $1 and d.sent_at is null`, serviceID)
}

func reportWith(status adapters.Health) adapters.Report {
	rep := testReport("")
	rep.Global = status
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
// one target of an alert. A nil component is the vendor's own status, which is
// what the target rows use to mean it.
func queueDeliveryFor(t *testing.T, st *Store, token string, componentID *int64) {
	t.Helper()

	if _, err := st.pool.Exec(context.Background(), `
		insert into alert_deliveries (channel_id, target_id, from_status, to_status, changed_at)
		select c.id, t.id, 'ok', 'major', now()
		from alert_channels c
		join alert_targets t on t.subscription_id = c.subscription_id
		where c.token = $1 and t.component_id is not distinct from $2`, token, componentID); err != nil {
		t.Fatalf("queue a delivery: %v", err)
	}
}

func TestSubscribeAlertKeepsOneRowPerAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert once"))

	if err := st.SubscribeAlert(ctx, id, nil, "ann@example.com", "token-one", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// The address and the vendor are the subscription, so asking twice is not
	// two subscriptions, and the token already in an inbox keeps working.
	if err := st.SubscribeAlert(ctx, id, nil, "ann@example.com", "token-two", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "bob@example.com", "token-bob", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "bob@example.com", "token-ignored", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "cara@example.com", "token-cara", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries d`+
		` join alert_targets t on t.id = d.target_id`+
		` where d.sent_at is null and t.service_id = $1`, id); n != 1 {
		t.Fatalf("pending deliveries = %d, want 1", n)
	}

	if err := st.UnsubscribeAlert(ctx, "token-cara"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries d`+
		` join alert_targets t on t.id = d.target_id where t.service_id = $1`, id); n != 0 {
		t.Errorf("deliveries = %d, want the waiting one dropped", n)
	}
}

func TestApplyReportQueuesOneAlertPerChange(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert change"))
	if err := st.SubscribeAlert(ctx, id, nil, "dave@example.com", "token-dave", WholeVendor); err != nil {
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

	pending, err := st.PendingAlerts(ctx, 3, 10)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want one alert", len(pending))
	}

	alert := pending[0]
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
	if err := st.SentAlert(ctx, alert.ID); err != nil {
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

func TestPendingAlertsLeavesUnsubscribedAddressesAlone(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert quiet"))
	if err := st.SubscribeAlert(ctx, id, nil, "erin@example.com", "token-erin", WholeVendor); err != nil {
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
		`update services set owner_id = $2 where id = $1`, id, owner.ID); err != nil {
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

	if err := st.SubscribeAlert(ctx, first, nil, "gina@example.com", "token-gina", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, second, nil, "gina@example.com", "token-gina-two", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, first, &user.ID, "work@example.org", "token-list-one", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SubscribeAlert(ctx, second, &user.ID, "home@example.net", "token-list-two", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// Somebody else's alert is not in this account's list.
	if err := st.SubscribeAlert(ctx, first, nil, "guest@example.com", "token-list-guest", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-pause", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-delete", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "guest@example.com", "token-guest", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert for a guest's alert = %v, want ErrNotFound", err)
	}

	// One made while signed in does, even when the address is not the account's.
	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-work", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-work", WholeVendor); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "frank@example.com", "token-frank", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "frank@example.com", "token-frank", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert signed in: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, "frank@example.com", "token-frank", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-mail", WholeVendor); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "component@example.org",
		"token-component", AlertTargets{Components: []int64{componentID}}); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, nil, "watching-service@example.org",
		"token-whole", WholeVendor); err != nil {
		t.Fatalf("subscribe to the vendor: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, "watching-component@example.org",
		"token-part", AlertTargets{Components: []int64{componentID}}); err != nil {
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

// Asking again is changing your mind. What the subscription watches becomes what
// was asked for this time, so a component can be added or dropped from the bell;
// the channel keeps the token an email already carries, and a change of mind does
// not throw away what is already waiting for something still watched.
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, email, "token-mind", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if err := st.SubscribeAlert(ctx, id, &user.ID, email, "token-unused",
		AlertTargets{Components: []int64{first, second}}); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, &user.ID, email, "token-unused",
		AlertTargets{Components: []int64{first}}); err != nil {
		t.Fatalf("drop one component: %v", err)
	}
	if n := queuedForToken(t, st, "token-mind"); n != 1 {
		t.Errorf("pending = %d, want the one for the component still watched", n)
	}

	// And the whole vendor can be picked again, which takes the components with
	// it: one thing watched, not three.
	if err := st.SubscribeAlert(ctx, id, &user.ID, email, "token-unused", WholeVendor); err != nil {
		t.Fatalf("change back: %v", err)
	}
	if n := countRows(t, st, targets); n != 1 {
		t.Errorf("targets = %d, want only the vendor once it is picked again", n)
	}
}

// Purging a vendor takes the alert that only watched it, and leaves one that
// also watches another vendor: an alert is about what it watches, and a
// subscription with nothing left would sit on its address and token for ever.
func TestPurgingAVendorTakesTheAlertThatOnlyWatchedIt(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	goneSlug := uniqueName("alert purge gone")
	keptSlug := uniqueName("alert purge kept")
	gone := alertService(t, st, goneSlug)
	kept := alertService(t, st, keptSlug)

	if err := st.SubscribeAlert(ctx, gone, nil, "anna@example.com", "token-purge-gone", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SubscribeAlert(ctx, kept, nil, "bella@example.com", "token-purge-kept", WholeVendor); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	// Somebody who follows both vendors, which is what one subscription with two
	// targets is for.
	var bothID int64
	if err := st.pool.QueryRow(ctx, `
		with made as (
			insert into alert_subscriptions (user_id) values (null) returning id
		)
		insert into alert_targets (subscription_id, service_id)
		select made.id, $1 from made
		returning subscription_id`, gone).Scan(&bothID); err != nil {
		t.Fatalf("create a subscription watching both: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		insert into alert_targets (subscription_id, service_id) values ($1, $2)`,
		bothID, kept); err != nil {
		t.Fatalf("add the second target: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		insert into alert_channels (subscription_id, kind, address, token)
		values ($1, 'email', 'cara@example.com', $2)`, bothID, uniqueName("token-both")); err != nil {
		t.Fatalf("add the channel: %v", err)
	}

	if err := st.PurgeService(ctx, goneSlug); err != nil {
		t.Fatalf("PurgeService: %v", err)
	}

	if n := countRows(t, st,
		`select count(*) from alert_channels where token = $1`, "token-purge-gone"); n != 0 {
		t.Error("the alert that only watched the purged vendor is still there")
	}
	if n := countRows(t, st,
		`select count(*) from alert_channels where token = $1`, "token-purge-kept"); n != 1 {
		t.Errorf("the alert on the other vendor = %d, want it kept", n)
	}
	if n := countRows(t, st,
		`select count(*) from alert_targets where subscription_id = $1`, bothID); n != 1 {
		t.Errorf("targets left = %d, want the other vendor's", n)
	}
}
