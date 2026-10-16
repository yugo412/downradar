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
		join alert_subscriptions a on a.id = d.subscription_id
		where a.service_id = $1 and d.sent_at is null`, serviceID)
}

func reportWith(status adapters.Health) adapters.Report {
	rep := testReport("")
	rep.Global = status
	return rep
}

func TestSubscribeAlertKeepsOneRowPerAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert once"))

	if err := st.SubscribeAlert(ctx, id, nil, "ann@example.com", "token-one"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// The address is the subscription, so asking twice is not two subscriptions,
	// and the token already in an inbox keeps working.
	if err := st.SubscribeAlert(ctx, id, nil, "ann@example.com", "token-two"); err != nil {
		t.Fatalf("SubscribeAlert again: %v", err)
	}

	if n := countRows(t, st, `select count(*) from alert_subscriptions where service_id = $1`, id); n != 1 {
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
	if err := st.SubscribeAlert(ctx, id, nil, "bob@example.com", "token-bob"); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "bob@example.com", "token-ignored"); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "cara@example.com", "token-cara"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}

	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthOK)); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := st.ApplyReport(ctx, id, reportWith(adapters.HealthMajor)); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries where sent_at is null`+
		` and subscription_id = (select id from alert_subscriptions where service_id = $1)`, id); n != 1 {
		t.Fatalf("pending deliveries = %d, want 1", n)
	}

	if err := st.UnsubscribeAlert(ctx, "token-cara"); err != nil {
		t.Fatalf("UnsubscribeAlert: %v", err)
	}
	if n := countRows(t, st, `select count(*) from alert_deliveries where subscription_id =`+
		` (select id from alert_subscriptions where service_id = $1)`, id); n != 0 {
		t.Errorf("deliveries = %d, want the waiting one dropped", n)
	}
}

func TestApplyReportQueuesOneAlertPerChange(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	id := alertService(t, st, uniqueName("alert change"))
	if err := st.SubscribeAlert(ctx, id, nil, "dave@example.com", "token-dave"); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "erin@example.com", "token-erin"); err != nil {
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
	if _, err := st.pool.Exec(ctx,
		`update alert_subscriptions set unsubscribed_at = now() where service_id = $1`, id); err != nil {
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

	if err := st.SubscribeAlert(ctx, first, nil, "gina@example.com", "token-gina"); err != nil {
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
	if err := st.SubscribeAlert(ctx, second, nil, "gina@example.com", "token-gina-two"); err != nil {
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

	if err := st.SubscribeAlert(ctx, first, &user.ID, "work@example.org", "token-list-one"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if err := st.SubscribeAlert(ctx, second, &user.ID, "home@example.net", "token-list-two"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	// Somebody else's alert is not in this account's list.
	if err := st.SubscribeAlert(ctx, first, nil, "guest@example.com", "token-list-guest"); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-pause"); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-delete"); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "guest@example.com", "token-guest"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	if _, err := st.UserAlert(ctx, user.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserAlert for a guest's alert = %v, want ErrNotFound", err)
	}

	// One made while signed in does, even when the address is not the account's.
	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-work"); err != nil {
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
		`select unsubscribed_at is null from alert_subscriptions where token = 'token-guest'`).Scan(&guestStill); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "work@example.org", "token-work"); err != nil {
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
	if err := st.SubscribeAlert(ctx, id, nil, "frank@example.com", "token-frank"); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	var userID *int64
	if err := st.pool.QueryRow(ctx,
		`select user_id from alert_subscriptions where token = 'token-frank'`).Scan(&userID); err != nil {
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

	if err := st.SubscribeAlert(ctx, id, &user.ID, "frank@example.com", "token-frank"); err != nil {
		t.Fatalf("SubscribeAlert signed in: %v", err)
	}
	if err := st.SubscribeAlert(ctx, id, nil, "frank@example.com", "token-frank"); err != nil {
		t.Fatalf("SubscribeAlert signed out again: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`select user_id from alert_subscriptions where token = 'token-frank'`).Scan(&userID); err != nil {
		t.Fatalf("read user_id: %v", err)
	}
	if userID == nil || *userID != user.ID {
		t.Errorf("user_id = %v, want the id it was given", userID)
	}
}
