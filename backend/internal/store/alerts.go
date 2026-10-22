package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The three things a subscription can be doing. They are stored as two
// timestamps on its channels, and this is what the two of them say.
const (
	AlertActive       = "active"
	AlertPaused       = "paused"
	AlertUnsubscribed = "unsubscribed"
)

// The ways an alert can arrive, as the channel's kind column holds them. Email,
// Telegram, Slack and Discord report on what was picked; a webhook hears about
// the whole vendor.
const (
	ChannelEmail    = "email"
	ChannelWebhook  = "webhook"
	ChannelTelegram = "telegram"
	ChannelSlack    = "slack"
	ChannelDiscord  = "discord"
)

// The two kinds of webhook call that are written down: one the sender made for a
// change, and one somebody tried from the page before saving anything.
const (
	CallDelivery = "delivery"
	CallTest     = "test"
)

// How a webhook says a call is ours. Standard signs every call with a secret
// both ends know, following the Standard Webhooks specification; custom sends
// the headers the subscriber wrote and nothing else, which is what a service
// that only takes a static header or a URL needs.
const (
	WebhookAuthStandard = "standard"
	WebhookAuthCustom   = "custom"
)

// WebhookSecretGrace is how long the secret a rotation retires keeps going out
// beside the new one. It is what makes a rotation something a receiver can keep
// up with rather than an outage.
const WebhookSecretGrace = 24 * time.Hour

// ErrWebhookSecretMissing is what a save that asks for signed calls without a
// secret is refused with: one has to be made before a call can be signed.
var ErrWebhookSecretMissing = errors.New("the webhook has no signing secret")

// ErrWebhookSecretUnused is what a save is refused with when it asks for a secret
// for a webhook that is not signing its calls.
var ErrWebhookSecretUnused = errors.New("only a signed webhook has a secret")

// AlertStateValid reports whether a state is one a caller may ask for.
func AlertStateValid(state string) bool {
	switch state {
	case AlertActive, AlertPaused, AlertUnsubscribed:
		return true
	default:
		return false
	}
}

// AlertView is one subscription: what it is for, where it goes, and what it is
// doing. The address is carried so a handler can mask it or show it, and the id
// so the account screen can act on the row. VendorStatus is the vendor's own
// health, which the account screen shows so a row says more than its name.
//
// A subscription keeps no state of its own. PausedAt and UnsubscribedAt are what
// its channels say, because a channel is what decides whether a message can
// still go out: one that is live keeps the subscription active.
type AlertView struct {
	ID             int64      `json:"id"`
	VendorSlug     string     `json:"vendorSlug"`
	VendorName     string     `json:"vendorName"`
	VendorStatus   string     `json:"vendorStatus,omitempty"`
	Email          string     `json:"-"`
	CreatedAt      time.Time  `json:"createdAt"`
	PausedAt       *time.Time `json:"pausedAt,omitempty"`
	UnsubscribedAt *time.Time `json:"unsubscribedAt,omitempty"`

	// Whether a way that is set up is quiet. A way can be paused on its own,
	// which is how one of them is stopped without taking its address or its URL
	// away, so a page that offers to do that needs both.
	EmailPaused    bool `json:"emailPaused"`
	WebhookPaused  bool `json:"webhookPaused"`
	TelegramPaused bool `json:"telegramPaused"`
	SlackPaused    bool `json:"slackPaused"`
	DiscordPaused  bool `json:"discordPaused"`

	// Telegram is the chat a message goes to, and Kind is the kind of channel a
	// view is about when it is about one: the unsubscribe link in a message is
	// read through the channel's own token, and the page it opens says what it is
	// about to stop. TelegramBot is the bot that speaks to that chat, which is the
	// subscriber's own rather than the site's and is never handed back to a page.
	// Slack and Discord are the webhook URLs those messages go to.
	Telegram     string `json:"-"`
	TelegramBot  string `json:"-"`
	Slack        string `json:"-"`
	SlackChannel string `json:"-"`
	Discord      string `json:"-"`
	Kind         string `json:"-"`

	// The kinds of channel the subscription arrives by, in a stable order. The
	// ones that are still live are what a screen listing the rows shows, and a
	// subscription with none left falls back to the kinds it had, so an ended row
	// still says how it used to arrive.
	Channels []string `json:"channels,omitempty"`

	// What the subscription watches. WholeService is the vendor's own status;
	// ComponentIDs are the vendor's identifiers for the components beside it, in
	// the order they sit on the page, which is what a caller that has to tick the
	// boxes again needs, while Components carries their names for a screen that
	// only shows them.
	WholeService bool     `json:"wholeService"`
	ComponentIDs []string `json:"-"`
	Components   []string `json:"-"`

	// Webhook is the second way the subscription can arrive, which a page that
	// shows it filled in needs. It is nil when the alert does not call anywhere.
	Webhook *AlertWebhook `json:"webhook,omitempty"`
}

// State is what the row is doing now.
func (v AlertView) State() string {
	switch {
	case v.UnsubscribedAt != nil:
		return AlertUnsubscribed
	case v.PausedAt != nil:
		return AlertPaused
	default:
		return AlertActive
	}
}

// UserAlerts is everything one account has asked to be told about, newest first.
// Rows that are paused or were unsubscribed are part of the list: the point of
// the screen is to show them and offer to start them again. Each one carries the
// vendor's current health, so a row can say whether the thing it watches is up,
// and the components it watches beside it.
//
// A row is a subscription, which can watch several things and arrive several
// ways. It shows the kinds of channel it arrives by, and the address the email
// way goes to, which is empty for an alert that has no email, and it takes the
// first thing the subscription watches.
func (s *Store) UserAlerts(ctx context.Context, userID int64) ([]AlertView, error) {
	rows, err := s.pool.Query(ctx, `
		select a.id, v.slug, v.name, coalesce(ss.status, 'unknown'),
		       coalesce(ch.address, ''), a.created_at,
		       case when ch.total > 0 and ch.ended < ch.total and ch.live = 0
		            then ch.last_paused end,
		       case when ch.total > 0 and ch.ended = ch.total
		            then ch.last_ended end,
		       w.whole, w.components, ch.email_paused, ch.webhook_paused, ch.kinds
		from alert_subscriptions a
		join services v on v.id = a.service_id
		left join service_state ss on ss.service_id = v.id
		left join lateral (
			select count(*) filter (where t2.component_id is null) > 0 as whole,
			       coalesce(array_agg(cm.name order by cm.position, cm.name)
			                filter (where t2.component_id is not null), '{}'::text[]) as components
			from alert_targets t2
			left join components cm on cm.id = t2.component_id
			where t2.subscription_id = a.id
		) w on true
		left join lateral (
			select (array_agg(c.address order by (c.unsubscribed_at is null) desc, c.id)
			        filter (where c.kind = 'email'))[1] as address,
			       count(*) as total,
			       count(*) filter (where c.unsubscribed_at is not null) as ended,
			       count(*) filter (where c.unsubscribed_at is null and c.paused_at is null) as live,
			       max(c.unsubscribed_at) as last_ended,
			       max(c.paused_at) as last_paused,
			       count(*) filter (where c.kind = 'email' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as email_paused,
			       count(*) filter (where c.kind = 'webhook' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as webhook_paused,
			       coalesce(
			           array_agg(distinct c.kind order by c.kind) filter (where c.unsubscribed_at is null),
			           array_agg(distinct c.kind order by c.kind),
			           '{}'::text[]
			       ) as kinds
			from alert_channels c
			where c.subscription_id = a.id
		) ch on true
		where a.user_id = $1
		order by a.id desc`, userID)
	if err != nil {
		return nil, fmt.Errorf("alerts: list for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []AlertView
	for rows.Next() {
		var v AlertView
		if err := rows.Scan(&v.ID, &v.VendorSlug, &v.VendorName, &v.VendorStatus,
			&v.Email, &v.CreatedAt, &v.PausedAt, &v.UnsubscribedAt,
			&v.WholeService, &v.Components, &v.EmailPaused, &v.WebhookPaused,
			&v.Channels); err != nil {
			return nil, fmt.Errorf("alerts: scan list: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetChannelPausedByToken stops or starts the one channel a link came from. It is
// the same switch the page offers a way, reached from a message rather than from a
// signed in screen, so the token is the whole of what proves it: the link is the
// channel. What was waiting for it is dropped, the same way the page's switch does
// it, and a channel that was stopped for good cannot be started again by it.
func (s *Store) SetChannelPausedByToken(ctx context.Context, token string, paused bool) error {
	var moved int
	err := s.pool.QueryRow(ctx, `
		with mine as (
			select id from alert_channels
			where token = $1 and unsubscribed_at is null
		), moved as (
			update alert_channels
			set paused_at = case when $2 then coalesce(paused_at, now()) else null end,
			    updated_at = now()
			where id in (select id from mine)
			returning id
		), dropped as (
			delete from alert_deliveries
			where $2 and sent_at is null and channel_id in (select id from mine)
		)
		select count(*) from moved`, token, paused).Scan(&moved)
	if err != nil {
		return fmt.Errorf("alerts: pause a channel by its link: %w", err)
	}
	if moved == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserAlertState moves one of an account's own subscriptions between active,
// paused and unsubscribed. Every channel it has is moved with it: what the
// screen calls pausing is the whole subscription going quiet, while the link in
// one message stops only the channel that message came from. Anything waiting is
// dropped, because the point of both is that nothing arrives, and resuming
// should not deliver last week's news.
func (s *Store) SetUserAlertState(ctx context.Context, userID, id int64, state string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mine int64
	err = tx.QueryRow(ctx, `
		update alert_subscriptions set updated_at = now()
		where id = $1 and user_id = $2
		returning id`, id, userID).Scan(&mine)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("alerts: set state for %d: %w", id, err)
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels set
			unsubscribed_at = case when $2 = 'unsubscribed' then now() else null end,
			paused_at       = case when $2 = 'paused' then now() else null end,
			updated_at      = now()
		where subscription_id = $1`, id, state); err != nil {
		return fmt.Errorf("alerts: set state for %d: %w", id, err)
	}

	if state != AlertActive {
		if _, err := tx.Exec(ctx, `
			delete from alert_deliveries
			where sent_at is null
			  and channel_id in (select id from alert_channels where subscription_id = $1)`,
			id); err != nil {
			return fmt.Errorf("alerts: drop pending deliveries: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("alerts: set state commit: %w", err)
	}
	return nil
}

// DeleteUserAlert removes one of an account's own subscriptions for good, and
// with it what it watched and how it arrived. The unsubscribe link keeps a row
// so the same address can be subscribed again; this is for somebody who wants it
// gone from the screen.
func (s *Store) DeleteUserAlert(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`delete from alert_subscriptions where id = $1 and user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("alerts: delete %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetChannelPaused stops one way of an alert arriving without taking what it was
// told away: the address or the URL stays where it is, so starting it again is
// one press rather than typing it all back in. What is waiting for that way is
// dropped, because the point of pausing is that nothing more arrives by it.
//
// The subscription itself is left alone. Pausing from the account screen is the
// whole thing going quiet, which is every way it has; this is one of them.
func (s *Store) SetChannelPaused(ctx context.Context, userID, serviceID int64, kind string, paused bool) error {
	var moved int
	err := s.pool.QueryRow(ctx, `
		with mine as (
			select c.id
			from alert_channels c
			join alert_subscriptions a on a.id = c.subscription_id
			where a.user_id = $1 and a.service_id = $2
			  and c.kind = $3 and c.unsubscribed_at is null
		), moved as (
			update alert_channels
			set paused_at = case when $4 then coalesce(paused_at, now()) else null end,
			    updated_at = now()
			where id in (select id from mine)
			returning id
		), dropped as (
			delete from alert_deliveries
			where $4 and sent_at is null and channel_id in (select id from mine)
		)
		select count(*) from moved`, userID, serviceID, kind, paused).Scan(&moved)
	if err != nil {
		return fmt.Errorf("alerts: pause the %s way of service %d: %w", kind, serviceID, err)
	}
	if moved == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingAlert is one message waiting to go out. It carries the change it is
// about, so an alert describes the moment it happened even if the status has
// moved on by the time the far end takes it. ComponentName is set when the
// subscription watches one component of the vendor rather than the vendor
// itself, and the message says which one it was about.
//
// Kind is how this one goes out: an email carries Email, a chat its id and bot,
// a Slack or Discord channel its webhook URL, and a webhook its call.
type PendingAlert struct {
	ID          int64
	Kind        string
	Email       string
	ChatID      string
	TelegramBot string
	SlackURL    string
	DiscordURL  string
	Webhook     AlertWebhook
	ServiceID   int64
	ChannelID   int64
	// UserID is the account the subscription belongs to, which is who a webhook
	// call is recorded for so the page can show what happened. It is nil for a
	// subscription whose owner is gone, which has nobody to show a log to.
	UserID        *int64
	VendorName    string
	VendorSlug    string
	ComponentName string
	Token         string
	FromStatus    string
	ToStatus      string
	ChangedAt     time.Time
	Attempts      int
}

// PublicService returns the id and name of a vendor the open API can serve,
// which is the only kind an alert can be subscribed to: the email links to the
// vendor page, and a private service has none.
func (s *Store) PublicService(ctx context.Context, slug string) (int64, string, error) {
	var id int64
	var name string
	err := s.pool.QueryRow(ctx, `
		select id, name from services
		where slug = $1 and active and status = 'published' and visibility = 'public'`,
		slug,
	).Scan(&id, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("alerts: public service %s: %w", slug, err)
	}
	return id, name, nil
}

// AlertTargets is what the ways that report on a pick send about: the vendor's
// own status, or named components of it, or both. A component is named by its row
// id, which is what a target points at. A webhook has no targets of its own: it
// hears about the vendor whatever is picked here.
type AlertTargets struct {
	Whole      bool
	Components []int64
}

// WholeVendor is the alert the bell used to be the only kind of: the vendor's
// overall status and nothing else.
var WholeVendor = AlertTargets{Whole: true}

// AlertField is one name and value in a webhook: a header. The value may carry
// placeholders, which the sender fills in with what changed.
type AlertField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AlertWebhook is an alert's second way of arriving: where to call, and what to
// send with it. URL is the channel's own address column, and the rest is its
// settings, which is where the row always said a webhook's headers would live.
//
// What a webhook carries in its body is not up to the subscriber: every call
// looks the same and says the changes the notifier writes from what it finds.
// Only the headers are theirs, which is enough to sign a call or point it at a
// queue of their own.
//
// Auth is how a call says it is ours. Signing is the secret that does it, and it
// stays here: a page is told whether one is set and never what it is.
type AlertWebhook struct {
	URL     string         `json:"url"`
	Method  string         `json:"method"`
	Headers []AlertField   `json:"headers"`
	Auth    string         `json:"auth"`
	Signed  bool           `json:"signed"`
	Signing WebhookSigning `json:"-"`
}

// WebhookSigning is the secret a signed call is made with. It is kept because
// every call has to be signed with it, and it is hidden from every answer: the
// subscriber sees it once, when it is made, and after that only the fact that
// one is set.
//
// Previous is the secret a rotation is retiring. It goes out beside the new one
// until PreviousUntil, which is long enough for a receiver to be changed over.
type WebhookSigning struct {
	Secret        string    `json:"secret,omitempty"`
	CreatedAt     time.Time `json:"createdAt,omitempty"`
	Previous      string    `json:"previous,omitempty"`
	PreviousUntil time.Time `json:"previousUntil,omitempty"`
}

// webhookSettings is the part of a webhook the settings column holds. The URL is
// the channel's address, so it is not repeated there.
type webhookSettings struct {
	Method  string          `json:"method"`
	Headers []AlertField    `json:"headers"`
	Auth    string          `json:"auth,omitempty"`
	Signing *WebhookSigning `json:"signing,omitempty"`
}

// NewWebhookSecret makes a secret in the shape the Standard Webhooks
// specification uses: the prefix "whsec_" and 24 random bytes in base64.
func NewWebhookSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("webhook secret: read random bytes: %w", err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(buf), nil
}

// WebhookSigningKey turns a secret into the bytes a call is signed with: the
// part after the prefix, decoded from base64.
func WebhookSigningKey(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return nil, fmt.Errorf("webhook secret: decode: %w", err)
	}
	return key, nil
}

// encodeWebhook encodes one webhook for the settings column.
func encodeWebhook(hook AlertWebhook) ([]byte, error) {
	settings := webhookSettings{Method: hook.Method, Headers: hook.Headers, Auth: hook.Auth}
	if hook.Signing.Secret != "" {
		signing := hook.Signing
		settings.Signing = &signing
	}
	return json.Marshal(settings)
}

// parseWebhookSettings reads back what encodeWebhook wrote. Any other settings,
// which is what an email channel carries, read as an empty webhook.
//
// A webhook saved before there were two ways to authenticate has no auth of its
// own, and it reads as custom: those are the headers it is already sending, and
// a change here would break a call that works today.
func parseWebhookSettings(raw []byte, address string) (AlertWebhook, error) {
	hook := AlertWebhook{URL: address, Auth: WebhookAuthCustom}
	if len(raw) == 0 {
		return hook, nil
	}

	var settings webhookSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return hook, fmt.Errorf("alerts: read webhook settings: %w", err)
	}
	hook.Method = settings.Method
	hook.Headers = settings.Headers
	if settings.Auth == WebhookAuthStandard {
		hook.Auth = WebhookAuthStandard
	}
	if settings.Signing != nil {
		hook.Signing = *settings.Signing
	}
	hook.Signed = hook.Signing.Secret != ""
	return hook, nil
}

// AlertChange is what one save asks for: the ways an alert should arrive, and
// what it should watch. A way that is named replaces what the alert had; a way
// that is nil is left as it is, which is what a request that says nothing about
// it means. An empty address, or a webhook with no URL, stops that way.
type AlertChange struct {
	// Email is the address the alert should go to. EmailToken is for the channel
	// a save creates; a channel that is already there keeps the token it has, so
	// a link in a message already sent still reaches it.
	Email      *string
	EmailToken string

	// Webhook is the other way an alert can arrive. A webhook hears about every
	// change of the vendor, its own status and any of its components, which is
	// why it has nothing to pick: its body says what the vendor looks like now.
	//
	// WebhookNewSecret asks for a fresh signing secret as part of the save. It is
	// the only way one is made, and the answer it comes back in is the only time
	// it can be read.
	Webhook          *AlertWebhook
	WebhookToken     string
	WebhookNewSecret bool

	// Telegram is the chat a message goes to, which is the third way an alert can
	// arrive. It reports on what was picked like an email does, and it belongs to
	// an account rather than to the address somebody typed, because a chat is not
	// something a visitor without an account has to hand. The bot that speaks to
	// it is the subscriber's own, so the token is saved with the channel.
	Telegram      *AlertTelegram
	TelegramToken string

	// Slack is the fourth way an alert can arrive: the incoming webhook URL of a
	// Slack channel. It reports on what was picked, like an email, because the
	// message names the component that moved.
	Slack      *AlertSlack
	SlackToken string

	// Discord is the fifth way: the incoming webhook URL of a Discord channel,
	// reporting on what was picked.
	Discord      *AlertDiscord
	DiscordToken string

	// Targets is what the ways that report on a pick send about: the vendor's own
	// status, the components of it that were picked, or both.
	Targets AlertTargets
}

// EmailOnly is the alert as the bell has always sent it: one address, and what it
// watches. It says nothing about a webhook, so one that is already there stays.
func EmailOnly(email, token string, want AlertTargets) AlertChange {
	return AlertChange{Email: &email, EmailToken: token, Targets: want}
}

// ComponentIDs resolves components named by the vendor's own identifiers, which
// is what a page has to hand, to the rows a target points at. The whole set is
// refused if any of them is not one of this vendor's live components, because a
// subscription is written against the list the subscriber was shown.
func (s *Store) ComponentIDs(ctx context.Context, serviceID int64, sourceIDs []string) ([]int64, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	asked := map[string]bool{}
	for _, id := range sourceIDs {
		asked[id] = true
	}

	rows, err := s.pool.Query(ctx, `
		select id from components
		where service_id = $1 and active and source_id = any($2)
		order by position, id`, serviceID, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("alerts: component ids: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("alerts: scan component id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(asked) {
		return nil, ErrNotFound
	}
	return out, nil
}

// AddressWatchesService says whether one address already has an alert on one
// vendor that can still arrive. Saving over it is somebody changing their mind
// about that vendor rather than asking for another one, which is what keeps the
// limit on a subscriber exact: what is already there does not count twice. An
// address that stopped listening is not holding a place, so starting it again
// asks for one.
func (s *Store) AddressWatchesService(ctx context.Context, email string, serviceID int64) (bool, error) {
	var watching bool
	err := s.pool.QueryRow(ctx, `
		select exists (
			select 1
			from alert_subscriptions a
			join alert_channels c on c.subscription_id = a.id
			where a.service_id = $2 and c.kind = 'email' and c.address = $1
			  and c.unsubscribed_at is null)`,
		email, serviceID).Scan(&watching)
	if err != nil {
		return false, fmt.Errorf("alerts: address on service %d for %s: %w", serviceID, email, err)
	}
	return watching, nil
}

// UserWatchesService says whether an account already has an alert on one vendor
// that can still arrive. It is asked beside the address check because an alert
// can arrive without an address at all: a webhook carries no email, and saving
// one over an alert the account already has on this vendor takes no new place.
func (s *Store) UserWatchesService(ctx context.Context, userID, serviceID int64) (bool, error) {
	var watching bool
	err := s.pool.QueryRow(ctx, `
		select exists (
			select 1
			from alert_subscriptions a
			where a.user_id = $1 and a.service_id = $2
			  and exists (
				select 1 from alert_channels c
				where c.subscription_id = a.id and c.unsubscribed_at is null))`,
		userID, serviceID).Scan(&watching)
	if err != nil {
		return false, fmt.Errorf("alerts: account on service %d: %w", serviceID, err)
	}
	return watching, nil
}

// SubscribeAlert records what somebody wants to hear about one vendor. Asking
// again is changing their mind: the ways this save names replace what the alert
// had, and the set of targets becomes what was asked for, so what is no longer
// watched goes and anything still waiting for it goes with it. A channel that
// stays keeps its token, so a link in a message already sent still reaches this
// row, and saving clears a pause or an unsubscribing rather than leaving somebody
// with a way that is not being used.
//
// The targets are what the email reports on. A webhook hears about every change
// of the vendor, and its body says what the vendor looks like now, so it has no
// pick of its own to keep.
//
// The subscription is the one the account already has on this vendor, or the one
// the address holds, or a new one. The account's own comes first: a page shows one
// alert per vendor, and a save has to land on the row behind that page rather
// than make a second one beside it.
//
// A user id is only kept if one is given, so subscribing while signed out never
// clears a link that was made while signed in.
func (s *Store) SubscribeAlert(ctx context.Context, serviceID int64, userID *int64, change AlertChange) error {
	_, err := s.subscribeAlert(ctx, serviceID, userID, change)
	return err
}

// SubscribeAlertWithSecret is the same save, asking for a fresh signing secret as
// part of it. The secret is returned once and never again.
func (s *Store) SubscribeAlertWithSecret(ctx context.Context, serviceID int64, userID *int64, change AlertChange) (string, error) {
	change.WebhookNewSecret = true
	return s.subscribeAlert(ctx, serviceID, userID, change)
}

// subscribeAlert is the save both of those are: one transaction, so a secret is
// never left on a webhook the save did not finish writing.
func (s *Store) subscribeAlert(ctx context.Context, serviceID int64, userID *int64, change AlertChange) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	subscriptionID, err := alertSubscription(ctx, tx, serviceID, userID, change.Email)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `
			update alert_subscriptions
			set user_id = coalesce($2, user_id), updated_at = now()
			where id = $1`, subscriptionID, userID); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}

	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx,
			`insert into alert_subscriptions (user_id, service_id) values ($1, $2) returning id`,
			userID, serviceID).Scan(&subscriptionID); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}

	default:
		return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
	}

	if change.Email != nil {
		if err := saveEmailChannel(ctx, tx, subscriptionID, *change.Email, change.EmailToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	secret := ""
	if change.Webhook != nil {
		secret, err = saveWebhookChannel(ctx, tx, subscriptionID, *change.Webhook,
			change.WebhookToken, change.WebhookNewSecret)
		if err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Telegram != nil {
		if err := saveTelegramChannel(ctx, tx, subscriptionID, *change.Telegram, change.TelegramToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Slack != nil {
		if err := saveSlackChannel(ctx, tx, subscriptionID, *change.Slack, change.SlackToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Discord != nil {
		if err := saveDiscordChannel(ctx, tx, subscriptionID, *change.Discord, change.DiscordToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}

	if err := setAlertTargets(ctx, tx, subscriptionID, serviceID, change.Targets); err != nil {
		return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("alerts: subscribe commit: %w", err)
	}
	return secret, nil
}

// alertSubscription is the row a save belongs on: the alert the account already
// has on this vendor if there is one, otherwise the one the address holds. Both
// are rows on this vendor whatever their channels are doing, because a paused or
// stopped way is still the row somebody is changing their mind about.
func alertSubscription(ctx context.Context, tx pgx.Tx, serviceID int64, userID *int64, email *string) (int64, error) {
	address := ""
	if email != nil {
		address = *email
	}

	var id int64
	err := tx.QueryRow(ctx, `
		select a.id
		from alert_subscriptions a
		where a.service_id = $1
		  and ((a.user_id is not null and a.user_id = $2)
		       or ($3 <> '' and exists (
				select 1 from alert_channels c
				where c.subscription_id = a.id and c.kind = 'email' and c.address = $3)))
		order by (a.user_id is not null and a.user_id = $2) desc, a.id
		limit 1`, serviceID, userID, address).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// saveEmailChannel makes the subscription's email way say what this save asked
// for. An empty address stops the emails rather than leaving a way with nowhere
// to go: a page shows one address, and clearing it is how somebody says they
// would rather not be emailed about this vendor at all.
//
// A subscription ends up with one email channel. An older build could leave an
// address with two on one vendor, when somebody saved a second address while
// signed in, and the one this save was given is the one that stays.
func saveEmailChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, address, token string) error {
	if address == "" {
		return stopChannels(ctx, tx, subscriptionID, ChannelEmail)
	}

	var channelID int64
	err := tx.QueryRow(ctx, `
		select id from alert_channels
		where subscription_id = $1 and kind = 'email'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&channelID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, token)
			values ($1, 'email', $2, $3)`, subscriptionID, address, token); err != nil {
			return fmt.Errorf("add the email way: %w", err)
		}

	case err != nil:
		return fmt.Errorf("read the email way: %w", err)

	default:
		if _, err := tx.Exec(ctx, `
			update alert_channels
			set address = $2, unsubscribed_at = null, updated_at = now()
			where id = $1`, channelID, address); err != nil {
			return fmt.Errorf("update the email way: %w", err)
		}
		return dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelEmail, channelID)
	}
	return nil
}

// webhookChannel is the subscription's webhook row as it is now: where it calls,
// the token that proves it is that channel, and the settings that carry the rest.
// A row id is never zero, so nothing found means nothing is there yet.
func webhookChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64) (int64, string, []byte, error) {
	var id int64
	var token string
	var settings []byte
	err := tx.QueryRow(ctx, `
		select id, token, coalesce(settings, '{}'::jsonb)
		from alert_channels
		where subscription_id = $1 and kind = 'webhook'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&id, &token, &settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil, nil
	}
	if err != nil {
		return 0, "", nil, fmt.Errorf("read the webhook: %w", err)
	}
	return id, token, settings, nil
}

// saveWebhookChannel makes the subscription's webhook say what this save asked
// for. A webhook with no URL stops the calls rather than calling somewhere empty.
// The header list is entirely the subscriber's: nothing is added to it here.
//
// A save that says nothing about the secret keeps the one that is there, which is
// what makes saving the page safe after a secret has been made: the secret is
// never typed into it, and a page that has not been told cannot hand it back.
// Asking for a new one is the only way a secret is made, and what comes back is
// the only time it can be read. The one it replaces keeps going out for a day, so
// a receiver has time to move over.
func saveWebhookChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, hook AlertWebhook, token string, newSecret bool) (string, error) {
	if hook.URL == "" {
		return "", stopChannels(ctx, tx, subscriptionID, ChannelWebhook)
	}

	channelID, channelToken, settings, err := webhookChannel(ctx, tx, subscriptionID)
	if err != nil {
		return "", err
	}
	if channelToken == "" {
		channelToken = token
	}

	current, err := parseWebhookSettings(settings, hook.URL)
	if err != nil {
		return "", err
	}
	if hook.Auth == "" {
		hook.Auth = current.Auth
	}

	made := ""
	if newSecret {
		if hook.Auth != WebhookAuthStandard {
			return "", ErrWebhookSecretUnused
		}
		if made, err = NewWebhookSecret(); err != nil {
			return "", err
		}
		hook.Signing = WebhookSigning{Secret: made, CreatedAt: time.Now().UTC()}
		if current.Signing.Secret != "" {
			hook.Signing.Previous = current.Signing.Secret
			hook.Signing.PreviousUntil = hook.Signing.CreatedAt.Add(WebhookSecretGrace)
		}
	} else if hook.Signing.Secret == "" {
		hook.Signing = current.Signing
	}
	if hook.Auth == WebhookAuthStandard && hook.Signing.Secret == "" {
		return "", ErrWebhookSecretMissing
	}

	encoded, err := encodeWebhook(hook)
	if err != nil {
		return "", fmt.Errorf("write the webhook: %w", err)
	}

	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, settings, token)
			values ($1, 'webhook', $2, $3, $4)`,
			subscriptionID, hook.URL, encoded, channelToken); err != nil {
			return "", fmt.Errorf("add the webhook: %w", err)
		}
		return made, nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, settings = $3, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, hook.URL, encoded); err != nil {
		return "", fmt.Errorf("update the webhook: %w", err)
	}
	return made, dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelWebhook, channelID)
}

// AlertTelegram is the third way an alert can arrive: a chat, and the bot that
// speaks to it. The bot belongs to the subscriber rather than to the site, so the
// token is what this way is set up with; it is kept with the channel rather than
// in configuration, and a page is never handed it back.
type AlertTelegram struct {
	ChatID   string
	BotToken string
}

// AlertSlack is the fourth way an alert can arrive: the incoming webhook URL of
// a Slack channel. The URL names the workspace and the channel it posts to, so it
// is the whole of what the way is set up with. Channel is the name Slack showed
// when the webhook was made in one click, which is only there to say where a
// message goes; a URL pasted by hand has none.
type AlertSlack struct {
	URL     string
	Channel string
}

// AlertDiscord is the fifth way: the incoming webhook URL of a Discord channel.
type AlertDiscord struct {
	URL string
}

type telegramSettings struct {
	BotToken string `json:"botToken"`
}

// encodeTelegram writes one chat's bot for the settings column.
func encodeTelegram(way AlertTelegram) ([]byte, error) {
	return json.Marshal(telegramSettings{BotToken: way.BotToken})
}

// parseTelegramSettings reads back what encodeTelegram wrote. Any other settings,
// which is what an email channel carries, read as no bot at all.
func parseTelegramSettings(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var settings telegramSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", fmt.Errorf("alerts: read telegram settings: %w", err)
	}
	return settings.BotToken, nil
}

type slackSettings struct {
	Channel string `json:"channel,omitempty"`
}

// encodeSlackSettings writes the name of the channel a webhook was made for. It
// is only a label for the page, not something a delivery reads.
func encodeSlackSettings(settings slackSettings) ([]byte, error) {
	return json.Marshal(settings)
}

// parseSlackSettings reads back what encodeSlackSettings wrote. A URL pasted by
// hand has no name, which reads as an empty one.
func parseSlackSettings(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var settings slackSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", fmt.Errorf("alerts: read slack settings: %w", err)
	}
	return settings.Channel, nil
}

// saveTelegramChannel makes the subscription's chat say what this save asked for.
// An empty chat stops the messages rather than leaving a way with nowhere to go,
// which is the same bargain an address has. A chat with no bot could never be
// sent to, so the save has to name one rather than leave a way that looks set up
// and is not. A channel that is already there keeps its token, so the link in a
// message already sent still stops it.
//
// One chat per subscription, like one address: a save is a person saying where
// they want to be told, and what they name is what they get.
func saveTelegramChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertTelegram, token string) error {
	if way.ChatID == "" {
		return stopChannels(ctx, tx, subscriptionID, ChannelTelegram)
	}
	if way.BotToken == "" {
		return ErrNoBotToken
	}

	settings, err := encodeTelegram(way)
	if err != nil {
		return fmt.Errorf("write the telegram way: %w", err)
	}

	var channelID int64
	var channelToken string
	err = tx.QueryRow(ctx, `
		select id, token from alert_channels
		where subscription_id = $1 and kind = 'telegram'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&channelID, &channelToken)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		channelToken = token
	case err != nil:
		return fmt.Errorf("read the telegram way: %w", err)
	}

	// A row id is never zero, so nothing found means nothing is there yet.
	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, settings, token)
			values ($1, 'telegram', $2, $3, $4)`,
			subscriptionID, way.ChatID, settings, channelToken); err != nil {
			return fmt.Errorf("add the telegram way: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, settings = $3, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, way.ChatID, settings); err != nil {
		return fmt.Errorf("update the telegram way: %w", err)
	}
	return dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelTelegram, channelID)
}

// saveSlackChannel makes the subscription's Slack way say what this save asked
// for. An empty URL stops the messages rather than posting nowhere, which is the
// same bargain an address has. A channel that is already there keeps its token,
// so the link in a message already sent still stops it.
//
// The channel name only comes from an install, so a save that carries the same
// URL without one keeps the name the install left. A URL pasted by hand has no
// name, and a URL that changed is a different channel.
//
// One Slack way per subscription, like one address: a save is a person saying
// where they want to be told, and what they name is what they get.
func saveSlackChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertSlack, token string) error {
	if way.URL == "" {
		return stopChannels(ctx, tx, subscriptionID, ChannelSlack)
	}

	var channelID int64
	var channelToken string
	var address string
	var raw []byte
	err := tx.QueryRow(ctx, `
		select id, token, address, coalesce(settings, '{}'::jsonb)
		from alert_channels
		where subscription_id = $1 and kind = 'slack'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&channelID, &channelToken, &address, &raw)

	channel := way.Channel
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		channelToken = token
	case err != nil:
		return fmt.Errorf("read the slack way: %w", err)
	case channel == "" && address == way.URL:
		channel, err = parseSlackSettings(raw)
		if err != nil {
			return err
		}
	}

	settings, err := encodeSlackSettings(slackSettings{Channel: channel})
	if err != nil {
		return fmt.Errorf("write the slack way: %w", err)
	}

	// A row id is never zero, so nothing found means nothing is there yet.
	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, settings, token)
			values ($1, 'slack', $2, $3, $4)`, subscriptionID, way.URL, settings, channelToken); err != nil {
			return fmt.Errorf("add the slack way: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, settings = $3, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, way.URL, settings); err != nil {
		return fmt.Errorf("update the slack way: %w", err)
	}
	return dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelSlack, channelID)
}

// saveDiscordChannel saves one Discord way. An empty URL stops it, and an
// existing channel keeps its token.
func saveDiscordChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertDiscord, token string) error {
	if way.URL == "" {
		return stopChannels(ctx, tx, subscriptionID, ChannelDiscord)
	}

	var channelID int64
	var channelToken string
	err := tx.QueryRow(ctx, `
		select id, token from alert_channels
		where subscription_id = $1 and kind = 'discord'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&channelID, &channelToken)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		channelToken = token
	case err != nil:
		return fmt.Errorf("read the discord way: %w", err)
	}

	// A row id is never zero, so nothing found means nothing is there yet.
	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, token)
			values ($1, 'discord', $2, $3)`, subscriptionID, way.URL, channelToken); err != nil {
			return fmt.Errorf("add the discord way: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, way.URL); err != nil {
		return fmt.Errorf("update the discord way: %w", err)
	}
	return dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelDiscord, channelID)
}

// SlackWebhook returns the incoming webhook URL a Slack way on this vendor is set
// up with, for a message tried from the page that does not ask for the URL again.
// It is empty when there is no Slack way to send to.
func (s *Store) SlackWebhook(ctx context.Context, userID, serviceID int64) (string, error) {
	var webhook string
	err := s.pool.QueryRow(ctx, `
		select c.address
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where a.user_id = $1 and a.service_id = $2 and c.kind = 'slack'
		order by (c.unsubscribed_at is null) desc, c.id
		limit 1`, userID, serviceID).Scan(&webhook)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("alerts: the slack webhook: %w", err)
	}
	return webhook, nil
}

// DiscordWebhook returns the saved webhook for a Discord way, or empty when there
// is none.
func (s *Store) DiscordWebhook(ctx context.Context, userID, serviceID int64) (string, error) {
	var webhook string
	err := s.pool.QueryRow(ctx, `
		select c.address
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where a.user_id = $1 and a.service_id = $2 and c.kind = 'discord'
		order by (c.unsubscribed_at is null) desc, c.id
		limit 1`, userID, serviceID).Scan(&webhook)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("alerts: the discord webhook: %w", err)
	}
	return webhook, nil
}

// TelegramBot returns the bot a chat on this vendor is set up with, for a message
// tried from the page that does not ask for the token again. It is empty when
// there is no chat to send to.
func (s *Store) TelegramBot(ctx context.Context, userID, serviceID int64) (string, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		select c.settings
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where a.user_id = $1 and a.service_id = $2 and c.kind = 'telegram'
		order by (c.unsubscribed_at is null) desc, c.id
		limit 1`, userID, serviceID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("alerts: the telegram bot: %w", err)
	}
	return parseTelegramSettings(raw)
}

// stopChannels ends every live channel of one kind on a subscription, and drops
// what was waiting for them: the point of stopping a way is that nothing more
// arrives by it.
func stopChannels(ctx context.Context, tx pgx.Tx, subscriptionID int64, kind string) error {
	rows, err := tx.Query(ctx, `
		update alert_channels
		set unsubscribed_at = now(), paused_at = null, updated_at = now()
		where subscription_id = $1 and kind = $2 and unsubscribed_at is null
		returning id`, subscriptionID, kind)
	if err != nil {
		return fmt.Errorf("stop the %s way: %w", kind, err)
	}

	var channels []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("stop the %s way: %w", kind, err)
		}
		channels = append(channels, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("stop the %s way: %w", kind, err)
	}

	if len(channels) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		delete from alert_deliveries
		where sent_at is null and channel_id = any($1)`, channels); err != nil {
		return fmt.Errorf("drop what was waiting for the %s way: %w", kind, err)
	}
	return nil
}

// dropChannelsApartFrom leaves one channel of a kind on a subscription, which is
// what a save writes: one address and one webhook per alert. A channel that goes
// takes anything waiting for it, which is right for one that is being replaced.
func dropChannelsApartFrom(ctx context.Context, tx pgx.Tx, subscriptionID int64, kind string, keep int64) error {
	if _, err := tx.Exec(ctx, `
		delete from alert_channels
		where subscription_id = $1 and kind = $2 and id <> $3`,
		subscriptionID, kind, keep); err != nil {
		return fmt.Errorf("drop the other %s channels: %w", kind, err)
	}
	return nil
}

// setAlertTargets makes one subscription's messages report on exactly what was
// asked for. A target that is still wanted is left where it is, so a message
// already queued about it is not thrown away by an unrelated change of mind; the
// partial unique indexes are what says which of them is already there. What a
// target that goes takes with it is what was waiting to be sent about it, by
// address or by chat: the change was real, but the person has just said they do
// not want to hear about that thing. The calls are not affected, because a
// webhook hears about the whole vendor.
func setAlertTargets(ctx context.Context, tx pgx.Tx, subscriptionID, serviceID int64, want AlertTargets) error {
	// Written out rather than left to a foreign key, which is what dropped these
	// while a delivery pointed at the target it was for.
	if _, err := tx.Exec(ctx, `
		delete from alert_deliveries d
		using alert_channels c
		where c.subscription_id = $1 and c.kind in ('email', 'telegram')
		  and d.channel_id = c.id
		  and d.sent_at is null
		  and (($2 = false and d.component_id is null)
		       or (d.component_id is not null and d.component_id <> all(coalesce($3::bigint[], '{}'))))`,
		subscriptionID, want.Whole, want.Components); err != nil {
		return fmt.Errorf("drop what was waiting for the dropped targets: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		delete from alert_targets
		where subscription_id = $1
		  and (($2 = false and component_id is null)
		       or (component_id is not null and component_id <> all(coalesce($3::bigint[], '{}'))))`,
		subscriptionID, want.Whole, want.Components); err != nil {
		return fmt.Errorf("drop targets: %w", err)
	}

	if want.Whole {
		if _, err := tx.Exec(ctx, `
			insert into alert_targets (subscription_id, service_id)
			values ($1, $2)
			on conflict (subscription_id, service_id) where component_id is null do nothing`,
			subscriptionID, serviceID); err != nil {
			return fmt.Errorf("add the vendor target: %w", err)
		}
	}

	if len(want.Components) > 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_targets (subscription_id, service_id, component_id)
			select $1, $2, c from unnest($3::bigint[]) as c
			on conflict (subscription_id, component_id) where component_id is not null do nothing`,
			subscriptionID, serviceID, want.Components); err != nil {
			return fmt.Errorf("add the component targets: %w", err)
		}
	}

	return nil
}

// VendorRef names a vendor, which is all a caller needs to point somebody at it.
type VendorRef struct {
	Name string
	Slug string
}

// AlertElsewhere returns the vendor an address is already subscribed to, other
// than the one being asked about, or ErrNotFound when it has none. It is what a
// visitor who is not signed in is held to: with no account behind them, the
// address they typed is their whole identity, so its channels are the only thing
// there is to count. The vendor is named so the answer can say which one, rather
// than leaving somebody to guess.
//
// Only alerts that can still arrive count. A vendor that has been removed from
// the catalogue is not polled any more, so the alert on it is not holding the
// place that a second one would take.
func (s *Store) AlertElsewhere(ctx context.Context, email string, serviceID int64) (*VendorRef, error) {
	var ref VendorRef
	err := s.pool.QueryRow(ctx, `
		select v.name, v.slug
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		join services v on v.id = a.service_id
		where c.kind = 'email' and c.address = $1 and c.unsubscribed_at is null
		  and a.service_id <> $2
		  and v.active and v.status = 'published' and v.visibility = 'public'
		order by c.id
		limit 1`, email, serviceID).Scan(&ref.Name, &ref.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: elsewhere for %s: %w", email, err)
	}
	return &ref, nil
}

// WatchedServiceCount is how many vendors this account is watching, which is what
// the cap on a subscriber counts. Vendors are the unit rather than the things
// watched inside one: following a whole status page and three of its components
// is one vendor, and an address hearing about a vendor by email and later by
// webhook is one as well. A vendor removed from the catalogue is not polled any
// more, so its alert is not holding a place. Guests are held by AlertElsewhere
// instead, and staff are not held at all.
func (s *Store) WatchedServiceCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		select count(*)
		from alert_subscriptions a
		join services v on v.id = a.service_id
		where a.user_id = $1
		  and v.active and v.status = 'published' and v.visibility = 'public'
		  and exists (
			select 1 from alert_channels c
			where c.subscription_id = a.id and c.unsubscribed_at is null
		  )`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("alerts: watched services for user %d: %w", userID, err)
	}
	return count, nil
}

// AlertByToken resolves the token in an unsubscribe link. The link belongs to a
// channel, so this is what the page says it is about rather than everything the
// person has.
func (s *Store) AlertByToken(ctx context.Context, token string) (*AlertView, error) {
	var view AlertView
	err := s.pool.QueryRow(ctx, `
		select v.slug, v.name, c.address, c.kind, c.created_at,
		       c.paused_at, c.unsubscribed_at
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		join services v on v.id = a.service_id
		where c.token = $1`, token,
	).Scan(&view.VendorSlug, &view.VendorName, &view.Email, &view.Kind,
		&view.CreatedAt, &view.PausedAt, &view.UnsubscribedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: by token: %w", err)
	}
	return &view, nil
}

// UnsubscribeAlert stops the alerts for one token, which is one channel: the
// same subscription can still reach the person another way. It is written to be
// safe to repeat: the second visit changes nothing and is not an error.
func (s *Store) UnsubscribeAlert(ctx context.Context, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	err = tx.QueryRow(ctx, `
		update alert_channels
		set unsubscribed_at = now(), paused_at = null, updated_at = now()
		where token = $1
		returning id`, token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("alerts: unsubscribe: %w", err)
	}

	// Anything still waiting for this channel is dropped: the point of
	// unsubscribing is that no further message arrives.
	if _, err := tx.Exec(ctx, `
		delete from alert_deliveries
		where channel_id = $1 and sent_at is null`, id); err != nil {
		return fmt.Errorf("alerts: drop pending deliveries: %w", err)
	}

	return tx.Commit(ctx)
}

// UserAlert returns the alert a signed in person holds on one vendor, or
// ErrNotFound when they hold none. It is what the bell reads to decide whether
// to offer an alert or to say it already has one, so it looks at the link to the
// account rather than at the address: the address they gave does not have to be
// the one on their account. A paused one still counts as held.
//
// It also says what the subscription watches, so the bell can open with the
// boxes the person left ticked. ComponentIDs are the vendor's own identifiers,
// which is what the page has to hand.
func (s *Store) UserAlert(ctx context.Context, userID, serviceID int64) (*AlertView, error) {
	var view AlertView
	var webhookAddress string
	var webhookSettings []byte
	var telegramSettings []byte
	var slackRaw []byte
	err := s.pool.QueryRow(ctx, `
		select a.id, v.slug, v.name, coalesce(ch.address, ''), a.created_at,
		       case when ch.total > 0 and ch.ended < ch.total and ch.live = 0
		            then ch.last_paused end,
		       case when ch.total > 0 and ch.ended = ch.total
		            then ch.last_ended end,
		       w.whole, w.components,
		       coalesce(ch.webhook_address, ''), coalesce(ch.webhook_settings, '{}'::jsonb),
		       coalesce(ch.telegram_address, ''), coalesce(ch.telegram_settings, '{}'::jsonb),
		       coalesce(ch.slack_address, ''), coalesce(ch.slack_settings, '{}'::jsonb),
		       coalesce(ch.discord_address, ''),
		       ch.email_paused, ch.webhook_paused, ch.telegram_paused, ch.slack_paused,
		       ch.discord_paused
		from alert_subscriptions a
		join services v on v.id = a.service_id
		left join lateral (
			select count(*) filter (where t2.component_id is null) > 0 as whole,
			       coalesce(array_agg(cm.source_id order by cm.position, cm.source_id)
			                filter (where t2.component_id is not null), '{}'::text[]) as components
			from alert_targets t2
			left join components cm on cm.id = t2.component_id
			where t2.subscription_id = a.id
		) w on true
		left join lateral (
			select (array_agg(c.address order by c.id)
			        filter (where c.kind = 'email' and c.unsubscribed_at is null))[1] as address,
			       (array_agg(c.address order by c.id)
			        filter (where c.kind = 'webhook' and c.unsubscribed_at is null))[1] as webhook_address,
			       (array_agg(c.settings order by c.id)
			        filter (where c.kind = 'webhook' and c.unsubscribed_at is null))[1] as webhook_settings,
			       (array_agg(c.address order by c.id)
			        filter (where c.kind = 'telegram' and c.unsubscribed_at is null))[1] as telegram_address,
			       (array_agg(c.settings order by c.id)
			        filter (where c.kind = 'telegram' and c.unsubscribed_at is null))[1] as telegram_settings,
			       (array_agg(c.address order by c.id)
			        filter (where c.kind = 'slack' and c.unsubscribed_at is null))[1] as slack_address,
			       (array_agg(c.settings order by c.id)
			        filter (where c.kind = 'slack' and c.unsubscribed_at is null))[1] as slack_settings,
			       (array_agg(c.address order by c.id)
			        filter (where c.kind = 'discord' and c.unsubscribed_at is null))[1] as discord_address,
			       count(*) as total,
			       count(*) filter (where c.unsubscribed_at is not null) as ended,
			       count(*) filter (where c.unsubscribed_at is null and c.paused_at is null) as live,
			       max(c.unsubscribed_at) as last_ended,
			       max(c.paused_at) as last_paused,
			       count(*) filter (where c.kind = 'email' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as email_paused,
			       count(*) filter (where c.kind = 'webhook' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as webhook_paused,
			       count(*) filter (where c.kind = 'telegram' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as telegram_paused,
			       count(*) filter (where c.kind = 'slack' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as slack_paused,
			       count(*) filter (where c.kind = 'discord' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as discord_paused
			from alert_channels c
			where c.subscription_id = a.id
		) ch on true
		where a.user_id = $1
		  and a.service_id = $2
		  and exists (
			select 1 from alert_channels c
			where c.subscription_id = a.id and c.unsubscribed_at is null
		  )
		order by a.id
		limit 1`, userID, serviceID,
	).Scan(&view.ID, &view.VendorSlug, &view.VendorName, &view.Email,
		&view.CreatedAt, &view.PausedAt, &view.UnsubscribedAt,
		&view.WholeService, &view.ComponentIDs,
		&webhookAddress, &webhookSettings,
		&view.Telegram, &telegramSettings,
		&view.Slack, &slackRaw,
		&view.Discord,
		&view.EmailPaused, &view.WebhookPaused, &view.TelegramPaused, &view.SlackPaused,
		&view.DiscordPaused)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: for user %d on service %d: %w", userID, serviceID, err)
	}

	if webhookAddress != "" {
		hook, err := parseWebhookSettings(webhookSettings, webhookAddress)
		if err != nil {
			return nil, err
		}
		view.Webhook = &hook
	}
	if view.Telegram != "" {
		bot, err := parseTelegramSettings(telegramSettings)
		if err != nil {
			return nil, err
		}
		view.TelegramBot = bot
	}
	if view.Slack != "" {
		channel, err := parseSlackSettings(slackRaw)
		if err != nil {
			return nil, err
		}
		view.SlackChannel = channel
	}
	return &view, nil
}

// UnsubscribeUserAlert stops every alert a signed in person holds on one vendor,
// and reports how many subscriptions that was. Alerts they made while signed out
// are left alone: those belong to the address, not to the account.
func (s *Store) UnsubscribeUserAlert(ctx context.Context, userID, serviceID int64) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("alerts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		update alert_channels set
			unsubscribed_at = now(), paused_at = null, updated_at = now()
		where unsubscribed_at is null
		  and subscription_id in (
			select a.id
			from alert_subscriptions a
			where a.user_id = $1 and a.service_id = $2)
		returning id, subscription_id`, userID, serviceID)
	if err != nil {
		return 0, fmt.Errorf("alerts: unsubscribe user %d: %w", userID, err)
	}

	var channels []int64
	subscriptions := map[int64]bool{}
	for rows.Next() {
		var channelID, subscriptionID int64
		if err := rows.Scan(&channelID, &subscriptionID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("alerts: scan unsubscribed: %w", err)
		}
		channels = append(channels, channelID)
		subscriptions[subscriptionID] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("alerts: unsubscribed rows: %w", err)
	}

	// Nothing waiting for those channels should still go out: the point of
	// unsubscribing is that no further message arrives.
	if len(channels) > 0 {
		if _, err := tx.Exec(ctx, `
			delete from alert_deliveries
			where sent_at is null and channel_id = any($1)`, channels); err != nil {
			return 0, fmt.Errorf("alerts: drop pending deliveries: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("alerts: unsubscribe commit: %w", err)
	}
	return len(subscriptions), nil
}

// PendingAlerts is the next batch for the sender, oldest first.
func (s *Store) PendingAlerts(ctx context.Context, maxAttempts, limit int) ([]PendingAlert, error) {
	rows, err := s.pool.Query(ctx, `
		select d.id, c.id, c.kind, c.address, c.settings, d.service_id, s.name, s.slug,
		       coalesce(cm.name, ''), c.token, a.user_id,
		       d.from_status, d.to_status, d.changed_at, d.attempts
		from alert_deliveries d
		join alert_channels c on c.id = d.channel_id
		join alert_subscriptions a on a.id = c.subscription_id
		join services s on s.id = d.service_id
		left join components cm on cm.id = d.component_id
		where d.sent_at is null and d.attempts < $1
		  and c.unsubscribed_at is null and c.paused_at is null
		order by d.id
		limit $2`, maxAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("alerts: pending: %w", err)
	}
	defer rows.Close()

	var out []PendingAlert
	for rows.Next() {
		var p PendingAlert
		var address string
		var settings []byte
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Kind, &address, &settings, &p.ServiceID,
			&p.VendorName, &p.VendorSlug, &p.ComponentName, &p.Token, &p.UserID,
			&p.FromStatus, &p.ToStatus, &p.ChangedAt, &p.Attempts); err != nil {
			return nil, fmt.Errorf("alerts: scan pending: %w", err)
		}

		// The address is the inbox for email, the chat for Telegram, the webhook
		// URL for Slack or Discord, and the URL for a webhook.
		switch p.Kind {
		case ChannelWebhook:
			hook, err := parseWebhookSettings(settings, address)
			if err != nil {
				return nil, err
			}
			p.Webhook = hook
		case ChannelTelegram:
			p.ChatID = address
			bot, err := parseTelegramSettings(settings)
			if err != nil {
				return nil, err
			}
			p.TelegramBot = bot
		case ChannelSlack:
			p.SlackURL = address
		case ChannelDiscord:
			p.DiscordURL = address
		default:
			p.Email = address
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ServiceComponent is one component of a vendor as a webhook body reports it:
// what it is called, which group it sits in, and the status it is in.
type ServiceComponent struct {
	Name   string
	Group  string
	Status string
}

// ServiceSnapshot is a vendor as it stands now: what it is called, the status it
// is in, and everything it reports under itself. A webhook body carries the whole
// of it rather than only the thing that changed, so a receiver can act on the
// state of the vendor rather than only on the news. The name and the slug are
// here for the one caller that has no delivery to read them from: a try from the
// page, which has a service and nothing else yet.
type ServiceSnapshot struct {
	Name       string
	Slug       string
	Status     string
	Components []ServiceComponent
}

// ServiceSnapshot reads one vendor's state and its components, in the order the
// vendor's own page shows them.
func (s *Store) ServiceSnapshot(ctx context.Context, serviceID int64) (ServiceSnapshot, error) {
	var snapshot ServiceSnapshot
	err := s.pool.QueryRow(ctx, `
		select s.name, s.slug, coalesce(ss.status, 'unknown')
		from services s
		left join service_state ss on ss.service_id = s.id
		where s.id = $1`, serviceID).Scan(&snapshot.Name, &snapshot.Slug, &snapshot.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, ErrNotFound
	}
	if err != nil {
		return snapshot, fmt.Errorf("alerts: state of service %d: %w", serviceID, err)
	}

	rows, err := s.pool.Query(ctx, `
		select c.name, coalesce(c.group_name, ''), coalesce(cs.status, 'unknown')
		from components c
		left join component_state cs on cs.component_id = c.id
		where c.service_id = $1 and c.active
		order by c.position, c.name`, serviceID)
	if err != nil {
		return snapshot, fmt.Errorf("alerts: components of service %d: %w", serviceID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var c ServiceComponent
		if err := rows.Scan(&c.Name, &c.Group, &c.Status); err != nil {
			return snapshot, fmt.Errorf("alerts: scan component: %w", err)
		}
		snapshot.Components = append(snapshot.Components, c)
	}
	return snapshot, rows.Err()
}

// WebhookCall is one call to a webhook that was made, and what came back. It is
// what the page shows beside the webhook it belongs to, so a subscriber can see
// that their end answers without asking anybody. The body that was sent and the
// headers are not kept: they are the subscriber's own words, and one of them may
// be a key.
type WebhookCall struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	Status    *int      `json:"status"`
	Response  string    `json:"response"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

// webhookCallsKept is how many calls one alert keeps. The page shows the last few
// of them, and older ones are dropped as new ones are written, so an alert that
// is called every day does not grow a table nobody reads.
const webhookCallsKept = 50

// LogWebhookCall writes one call down. A subscription with no owner has nobody to
// show a log to, so nothing is written for it rather than a row that no screen
// can reach.
func (s *Store) LogWebhookCall(ctx context.Context, userID *int64, serviceID int64, channelID *int64, call WebhookCall) error {
	if userID == nil {
		return nil
	}

	if _, err := s.pool.Exec(ctx, `
		insert into alert_webhook_calls
			(user_id, service_id, channel_id, kind, url, method, status, response, error)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		*userID, serviceID, channelID, call.Kind, call.URL, call.Method,
		call.Status, call.Response, call.Error); err != nil {
		return fmt.Errorf("alerts: record a webhook call: %w", err)
	}

	if _, err := s.pool.Exec(ctx, `
		delete from alert_webhook_calls
		where id in (
			select id from alert_webhook_calls
			where user_id = $1 and service_id = $2
			order by id desc
			offset $3)`, *userID, serviceID, webhookCallsKept); err != nil {
		return fmt.Errorf("alerts: trim webhook calls: %w", err)
	}
	return nil
}

// WebhookCalls reads the last calls of one alert, newest first, which is what the
// page shows beside its webhook.
func (s *Store) WebhookCalls(ctx context.Context, userID, serviceID int64, limit int) ([]WebhookCall, error) {
	rows, err := s.pool.Query(ctx, `
		select id, kind, url, method, status, response, error, created_at
		from alert_webhook_calls
		where user_id = $1 and service_id = $2
		order by id desc
		limit $3`, userID, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("alerts: webhook calls of service %d: %w", serviceID, err)
	}
	defer rows.Close()

	out := []WebhookCall{}
	for rows.Next() {
		var call WebhookCall
		if err := rows.Scan(&call.ID, &call.Kind, &call.URL, &call.Method, &call.Status,
			&call.Response, &call.Error, &call.CreatedAt); err != nil {
			return nil, fmt.Errorf("alerts: scan a webhook call: %w", err)
		}
		out = append(out, call)
	}
	return out, rows.Err()
}

// SentAlert marks one message as delivered.
func (s *Store) SentAlert(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set sent_at = now(), error = null
		where id = $1`, id); err != nil {
		return fmt.Errorf("alerts: mark sent: %w", err)
	}
	return nil
}

// FailedAlert records one failed attempt. The row keeps its error and its count,
// which is what stops a dead address from being retried for ever.
func (s *Store) FailedAlert(ctx context.Context, id int64, message string) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set attempts = attempts + 1, error = $2
		where id = $1`, id, message); err != nil {
		return fmt.Errorf("alerts: mark failed: %w", err)
	}
	return nil
}
