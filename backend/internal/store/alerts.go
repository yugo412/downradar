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
// Telegram, Slack, Discord and Lark report on what was picked; a webhook hears
// about the whole vendor.
const (
	ChannelEmail    = "email"
	ChannelWebhook  = "webhook"
	ChannelTelegram = "telegram"
	ChannelSlack    = "slack"
	ChannelDiscord  = "discord"
	ChannelLark     = "lark"
)

// The two kinds of webhook call that are written down: one the sender made for a
// change, and one somebody tried from the page before saving anything.
const (
	CallDelivery = "delivery"
	CallTest     = "test"
)

// How a webhook says a call is ours. Standard signs every call with a secret
// both ends know; custom sends only the headers the subscriber wrote, which is
// what a service needing a static header or URL takes.
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
// doing. Its state comes from its channels, since a live one keeps it active,
// and it carries VendorStatus so a row says more than the vendor's name.
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
	LarkPaused     bool `json:"larkPaused"`

	// Telegram is the chat a message goes to and Kind the channel a view is about.
	// TelegramBot is the subscriber's own bot, never handed back. Slack, Discord
	// and Lark are the webhook URLs those messages go to; LarkSecret signs them
	// when the bot requires it.
	Telegram     string `json:"-"`
	TelegramBot  string `json:"-"`
	Slack        string `json:"-"`
	SlackChannel string `json:"-"`
	Discord      string `json:"-"`
	Lark         string `json:"-"`
	LarkSecret   string `json:"-"`
	Kind         string `json:"-"`

	// The kinds of channel the subscription arrives by, in a stable order; an
	// ended row falls back to the kinds it had so it still says how it arrived.
	Channels []string `json:"channels,omitempty"`

	// What the subscription watches. WholeService is the vendor's own status;
	// ComponentIDs are the vendor's identifiers in page order, which is what a
	// caller reticking the boxes needs, while Components carries their names.
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

// UserAlerts is everything one account has asked to be told about, newest first,
// paused and unsubscribed rows included so the screen can offer to start them
// again. A row shows its channel kinds, its email address and what it watches.
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

// SetChannelPausedByToken stops or starts the one channel a link came from, so
// the token is the whole of what proves it. What was waiting is dropped, and a
// channel stopped for good cannot be started again by it.
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
// paused and unsubscribed, every channel with it; the link in one message stops
// only its channel. Anything waiting is dropped so resuming brings no old news.
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

// DeleteUserAlert removes one of an account's own subscriptions for good, with
// what it watched and how it arrived. An unsubscribe link keeps its row instead.
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

// SetChannelPaused stops one way of an alert arriving while keeping the address
// or URL, so starting it again is one press. What was waiting is dropped; the
// subscription itself is untouched, unlike pausing from the account screen.
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

// ChannelDeliveryStatus is how the last real delivery of one channel went. A test
// from the page is not a delivery and does not move it.
type ChannelDeliveryStatus struct {
	Kind      string     `json:"kind"`
	AttemptAt *time.Time `json:"attemptAt,omitempty"`
	SuccessAt *time.Time `json:"successAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// RecordChannelAttempt writes down how the last real delivery of a channel went.
// The error is capped because a far end can answer with something enormous.
func (s *Store) RecordChannelAttempt(ctx context.Context, channelID int64, message string) error {
	const limit = 1000
	if len(message) > limit {
		message = message[:limit]
	}
	if _, err := s.pool.Exec(ctx, `
		update alert_channels
		set last_attempt_at = now(),
		    last_success_at = case when $2 = '' then now() else last_success_at end,
		    last_error = nullif($2, '')
		where id = $1`, channelID, message); err != nil {
		return fmt.Errorf("alerts: record the channel attempt: %w", err)
	}
	return nil
}

// ChannelDeliveryStatuses reads the last result of every live channel of one
// alert, so the page can show it beside the channel that sent it.
func (s *Store) ChannelDeliveryStatuses(ctx context.Context, userID, serviceID int64) ([]ChannelDeliveryStatus, error) {
	rows, err := s.pool.Query(ctx, `
		select c.kind, c.last_attempt_at, c.last_success_at, coalesce(c.last_error, '')
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where a.user_id = $1 and a.service_id = $2 and c.unsubscribed_at is null
		order by c.kind`, userID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("alerts: channel status for user %d: %w", userID, err)
	}
	defer rows.Close()

	out := []ChannelDeliveryStatus{}
	for rows.Next() {
		var status ChannelDeliveryStatus
		if err := rows.Scan(&status.Kind, &status.AttemptAt, &status.SuccessAt, &status.Error); err != nil {
			return nil, fmt.Errorf("alerts: scan channel status: %w", err)
		}
		out = append(out, status)
	}
	return out, rows.Err()
}

// PendingAlert is one message waiting to go out, carrying the change it is about
// so it describes the moment it happened even if status has moved on. ComponentName
// names the watched component, empty for the vendor; Kind says how it goes.
type PendingAlert struct {
	ID          int64
	Kind        string
	Email       string
	ChatID      string
	TelegramBot string
	SlackURL    string
	DiscordURL  string
	LarkURL     string
	LarkSecret  string
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
	// GeneratedSummary is the vendor sentence stored when the state changed.
	// SummaryGeneratedAt says when it was written, so the sender can leave it out
	// if the row is older than the change the alert is about.
	GeneratedSummary   string
	SummaryGeneratedAt *time.Time
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
// own status, named components, or both, each component named by its row id. A
// webhook has no targets: it hears about the vendor whatever is picked here.
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
// send with it. The body is not the subscriber's to shape, only the headers; Auth
// says how a call is ours, and a signing secret is never handed back to a page.
type AlertWebhook struct {
	URL     string         `json:"url"`
	Method  string         `json:"method"`
	Headers []AlertField   `json:"headers"`
	Auth    string         `json:"auth"`
	Signed  bool           `json:"signed"`
	Signing WebhookSigning `json:"-"`
}

// WebhookSigning is the secret a signed call is made with. It is hidden from
// every answer: the subscriber sees it once, when it is made. Previous is the
// retiring secret, sent beside the new one until PreviousUntil.
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

// parseWebhookSettings reads back what encodeWebhook wrote. Other settings read
// as an empty webhook. A webhook saved before there were two ways to
// authenticate has no auth and reads as custom, which is what it already sends.
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

// AlertChange is what one save asks for: the ways an alert should arrive and what
// it should watch. A named way replaces what was there, nil leaves it alone, and
// an empty address or a webhook with no URL stops that way.
type AlertChange struct {
	// Email is the address the alert should go to. EmailToken is for the channel
	// a save creates; a channel that is already there keeps the token it has, so
	// a link in a message already sent still reaches it.
	Email      *string
	EmailToken string

	// Webhook is the other way an alert can arrive. It hears about every change
	// of the vendor, which is why it has nothing to pick: its body says what the
	// vendor looks like now. WebhookNewSecret asks for a fresh signing secret.
	Webhook          *AlertWebhook
	WebhookToken     string
	WebhookNewSecret bool

	// Telegram is the chat a message goes to, reporting on what was picked like
	// an email. It belongs to an account rather than a typed address, and the bot
	// is the subscriber's own, so the token is saved with the channel.
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

	// Lark is the sixth way: a custom bot's webhook URL and the secret that
	// signs a request when the group requires one.
	Lark      *AlertLark
	LarkToken string

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
// refused if any is not one of this vendor's live components.
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
// vendor that can still arrive, so saving over it changes that alert rather than
// asking for another and counts once. A stopped one holds no place.
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

// UserWatchesService is the same check for an account, asked beside the address
// one because an alert can arrive without an address: a webhook saves over the
// account's existing alert on this vendor and takes no new place.
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

// SubscribeAlert records what somebody wants to hear about one vendor; asking
// again replaces ways and targets, drops their waiting messages, keeps tokens
// and clears a pause. The account's row wins, and a user id is kept only if given.
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
		secret, err = s.saveWebhookChannel(ctx, tx, subscriptionID, *change.Webhook,
			change.WebhookToken, change.WebhookNewSecret)
		if err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Telegram != nil {
		if err := s.saveTelegramChannel(ctx, tx, subscriptionID, *change.Telegram, change.TelegramToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Slack != nil {
		if err := s.saveSlackChannel(ctx, tx, subscriptionID, *change.Slack, change.SlackToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Discord != nil {
		if err := saveDiscordChannel(ctx, tx, subscriptionID, *change.Discord, change.DiscordToken); err != nil {
			return "", fmt.Errorf("alerts: subscribe on service %d: %w", serviceID, err)
		}
	}
	if change.Lark != nil {
		if err := s.saveLarkChannel(ctx, tx, subscriptionID, *change.Lark, change.LarkToken); err != nil {
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

// alertSubscription is the row a save belongs on: the account's alert on this
// vendor if there is one, otherwise the address's. Both count whatever their
// channels are doing, because a paused or stopped way is still being changed.
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
// for; an empty address stops it. One email channel remains, the address this
// save was given, even if an older build had left two on one vendor.
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
// for. No URL stops it, and the headers are the subscriber's own. A save naming
// no secret keeps it; a new one comes only on request, with the old one signing
// for a day during the switch.
func (s *Store) saveWebhookChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, hook AlertWebhook, token string, newSecret bool) (string, error) {
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

	current, err := s.parseWebhookSettings(settings, hook.URL)
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
	sealed, err := s.sealSettings(encoded)
	if err != nil {
		return "", fmt.Errorf("seal the webhook: %w", err)
	}

	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, settings, token)
			values ($1, 'webhook', $2, $3, $4)`,
			subscriptionID, hook.URL, sealed, channelToken); err != nil {
			return "", fmt.Errorf("add the webhook: %w", err)
		}
		return made, nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, settings = $3, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, hook.URL, sealed); err != nil {
		return "", fmt.Errorf("update the webhook: %w", err)
	}
	return made, dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelWebhook, channelID)
}

// AlertTelegram is the third way an alert can arrive: a chat and the bot that
// speaks to it. The bot is the subscriber's own, so its token is kept with the
// channel rather than in configuration and is never handed back to a page.
type AlertTelegram struct {
	ChatID   string
	BotToken string
}

// AlertSlack is the fourth way an alert can arrive: a Slack channel's incoming
// webhook URL, which is the whole of what it is set up with. Channel is the name
// an install showed, only to say where a message goes; a pasted URL has none.
type AlertSlack struct {
	URL     string
	Channel string
}

// AlertDiscord is the fifth way: the incoming webhook URL of a Discord channel.
type AlertDiscord struct {
	URL string
}

// AlertLark is the sixth way: the custom bot webhook of a Lark group, and the
// secret the bot uses to sign a request when the group requires it.
type AlertLark struct {
	URL    string
	Secret string
}

type larkSettings struct {
	Secret string `json:"secret,omitempty"`
}

func encodeLarkSettings(settings larkSettings) ([]byte, error) {
	return json.Marshal(settings)
}

func parseLarkSettings(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	var settings larkSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", fmt.Errorf("alerts: read lark settings: %w", err)
	}
	return settings.Secret, nil
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

// saveTelegramChannel saves the chat. An empty chat stops it, and a chat with no
// bot is refused. A channel already there keeps its token. One chat per
// subscription, like one address.
func (s *Store) saveTelegramChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertTelegram, token string) error {
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
	settings, err = s.sealSettings(settings)
	if err != nil {
		return fmt.Errorf("seal the telegram way: %w", err)
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
// for. An empty URL stops it, and an existing channel keeps its token. A
// same-URL save without a name keeps the install's name. One Slack way per
// subscription, like one address.
func (s *Store) saveSlackChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertSlack, token string) error {
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
		channel, err = s.parseSlackSettings(raw)
		if err != nil {
			return err
		}
	}

	settings, err := encodeSlackSettings(slackSettings{Channel: channel})
	if err != nil {
		return fmt.Errorf("write the slack way: %w", err)
	}
	settings, err = s.sealSettings(settings)
	if err != nil {
		return fmt.Errorf("seal the slack way: %w", err)
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

// saveLarkChannel makes the subscription's Lark way say what this save asked for.
// An empty URL stops it, and a save of the same URL without a secret keeps the
// one the group already uses. A different URL starts that group's own secret, so
// the old bot's secret cannot be sent to a new one.
func (s *Store) saveLarkChannel(ctx context.Context, tx pgx.Tx, subscriptionID int64, way AlertLark, token string) error {
	if way.URL == "" {
		return stopChannels(ctx, tx, subscriptionID, ChannelLark)
	}

	var channelID int64
	var channelToken string
	var address string
	var raw []byte
	err := tx.QueryRow(ctx, `
		select id, token, address, coalesce(settings, '{}'::jsonb)
		from alert_channels
		where subscription_id = $1 and kind = 'lark'
		order by (unsubscribed_at is null) desc, id
		limit 1`, subscriptionID).Scan(&channelID, &channelToken, &address, &raw)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		channelToken = token
	case err != nil:
		return fmt.Errorf("read the lark way: %w", err)
	case way.Secret == "" && address == way.URL:
		way.Secret, err = s.parseLarkSettings(raw)
		if err != nil {
			return err
		}
	}

	settings, err := encodeLarkSettings(larkSettings{Secret: way.Secret})
	if err != nil {
		return fmt.Errorf("write the lark way: %w", err)
	}
	settings, err = s.sealSettings(settings)
	if err != nil {
		return fmt.Errorf("seal the lark way: %w", err)
	}

	// A row id is never zero, so nothing found means nothing is there yet.
	if channelID == 0 {
		if _, err := tx.Exec(ctx, `
			insert into alert_channels (subscription_id, kind, address, settings, token)
			values ($1, 'lark', $2, $3, $4)`, subscriptionID, way.URL, settings, channelToken); err != nil {
			return fmt.Errorf("add the lark way: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		update alert_channels
		set address = $2, settings = $3, unsubscribed_at = null, updated_at = now()
		where id = $1`, channelID, way.URL, settings); err != nil {
		return fmt.Errorf("update the lark way: %w", err)
	}
	return dropChannelsApartFrom(ctx, tx, subscriptionID, ChannelLark, channelID)
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

// LarkWebhook returns the saved URL and signing secret of a Lark way, for a
// message tried from the page after it has been saved.
func (s *Store) LarkWebhook(ctx context.Context, userID, serviceID int64) (string, string, error) {
	var address string
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		select c.address, coalesce(c.settings, '{}'::jsonb)
		from alert_channels c
		join alert_subscriptions a on a.id = c.subscription_id
		where a.user_id = $1 and a.service_id = $2 and c.kind = 'lark'
		order by (c.unsubscribed_at is null) desc, c.id
		limit 1`, userID, serviceID).Scan(&address, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("alerts: the lark webhook: %w", err)
	}
	secret, err := s.parseLarkSettings(raw)
	if err != nil {
		return "", "", err
	}
	return address, secret, nil
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
	return s.parseTelegramSettings(raw)
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
// asked for. A target still wanted keeps its queued messages; one that goes
// drops what waited by email or chat, and calls stay: a webhook sees everything.
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

// AlertElsewhere returns the other vendor an address is already subscribed to,
// or ErrNotFound; it is what holds a signed-out visitor, whose address is their
// identity. Only alerts that can still arrive count: a removed vendor holds none.
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

// WatchedServiceCount is how many vendors an account watches, which is what a
// subscriber's cap counts: vendors, not the things inside one, and a removed
// vendor holds no place. Guests use AlertElsewhere; staff are not held at all.
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
// ErrNotFound. It follows the account link rather than the address, counts a
// paused one, and says what is watched so the bell opens with its boxes ticked.
func (s *Store) UserAlert(ctx context.Context, userID, serviceID int64) (*AlertView, error) {
	var view AlertView
	var webhookAddress string
	var webhookSettings []byte
	var telegramSettings []byte
	var slackRaw []byte
	var larkRaw []byte
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
		       coalesce(ch.lark_address, ''), coalesce(ch.lark_settings, '{}'::jsonb),
		       ch.email_paused, ch.webhook_paused, ch.telegram_paused, ch.slack_paused,
		       ch.discord_paused, ch.lark_paused
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
			       (array_agg(c.address order by c.id)
			        filter (where c.kind = 'lark' and c.unsubscribed_at is null))[1] as lark_address,
			       (array_agg(c.settings order by c.id)
			        filter (where c.kind = 'lark' and c.unsubscribed_at is null))[1] as lark_settings,
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
			                          and c.paused_at is not null) > 0 as discord_paused,
			       count(*) filter (where c.kind = 'lark' and c.unsubscribed_at is null
			                          and c.paused_at is not null) > 0 as lark_paused
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
		&view.Lark, &larkRaw,
		&view.EmailPaused, &view.WebhookPaused, &view.TelegramPaused, &view.SlackPaused,
		&view.DiscordPaused, &view.LarkPaused)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("alerts: for user %d on service %d: %w", userID, serviceID, err)
	}

	if webhookAddress != "" {
		hook, err := s.parseWebhookSettings(webhookSettings, webhookAddress)
		if err != nil {
			return nil, err
		}
		view.Webhook = &hook
	}
	if view.Telegram != "" {
		bot, err := s.parseTelegramSettings(telegramSettings)
		if err != nil {
			return nil, err
		}
		view.TelegramBot = bot
	}
	if view.Slack != "" {
		channel, err := s.parseSlackSettings(slackRaw)
		if err != nil {
			return nil, err
		}
		view.SlackChannel = channel
	}
	if view.Lark != "" {
		secret, err := s.parseLarkSettings(larkRaw)
		if err != nil {
			return nil, err
		}
		view.LarkSecret = secret
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
		       d.from_status, d.to_status, d.changed_at, d.attempts,
		       coalesce(sm.summary, ''), sm.generated_at
		from alert_deliveries d
		join alert_channels c on c.id = d.channel_id
		join alert_subscriptions a on a.id = c.subscription_id
		join services s on s.id = d.service_id
		left join components cm on cm.id = d.component_id
		left join service_summaries sm on sm.service_id = d.service_id
		where d.sent_at is null and d.attempts < $1
		  and d.deliver_after <= now()
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
			&p.FromStatus, &p.ToStatus, &p.ChangedAt, &p.Attempts,
			&p.GeneratedSummary, &p.SummaryGeneratedAt); err != nil {
			return nil, fmt.Errorf("alerts: scan pending: %w", err)
		}

		// The address is the inbox for email, the chat for Telegram, the webhook
		// URL for Slack or Discord, and the URL for a webhook.
		switch p.Kind {
		case ChannelWebhook:
			hook, err := s.parseWebhookSettings(settings, address)
			if err != nil {
				return nil, err
			}
			p.Webhook = hook
		case ChannelTelegram:
			p.ChatID = address
			bot, err := s.parseTelegramSettings(settings)
			if err != nil {
				return nil, err
			}
			p.TelegramBot = bot
		case ChannelSlack:
			p.SlackURL = address
		case ChannelDiscord:
			p.DiscordURL = address
		case ChannelLark:
			p.LarkURL = address
			secret, err := s.parseLarkSettings(settings)
			if err != nil {
				return nil, err
			}
			p.LarkSecret = secret
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

// ServiceSnapshot is a vendor as it stands now: its name, status and everything
// it reports under itself. A webhook body carries all of it, not just what
// changed, so a receiver can act on state; the name and slug serve a page test.
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

// WebhookCall is one call to a webhook and what came back, shown beside it so a
// subscriber can see their end answers. The body and headers are not kept: they
// are the subscriber's own words, and one may be a key.
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

// WebhookCallsKept is how many calls one alert keeps. The page shows them twenty
// at a time, and older ones are dropped as new ones are written, so an alert that
// is called every day does not grow a table nobody reads.
const WebhookCallsKept = 50

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
			offset $3)`, *userID, serviceID, WebhookCallsKept); err != nil {
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

// SentAlert marks one message as delivered. The status and time have to match
// what was sent: a change that arrived while the call was in flight has replaced
// the row and still needs its own message.
func (s *Store) SentAlert(ctx context.Context, id int64, toStatus string, changedAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set sent_at = now(), error = null
		where id = $1 and to_status = $2 and changed_at = $3 and sent_at is null`,
		id, toStatus, changedAt); err != nil {
		return fmt.Errorf("alerts: mark sent: %w", err)
	}
	return nil
}

// FailedAlert records one failed attempt. The row keeps its error and its count,
// which is what stops a dead address from being retried for ever. A row that
// changed while the attempt was in flight is left for its newer state.
func (s *Store) FailedAlert(ctx context.Context, id int64, toStatus string, changedAt time.Time, message string) error {
	if _, err := s.pool.Exec(ctx, `
		update alert_deliveries set attempts = attempts + 1, error = $4
		where id = $1 and to_status = $2 and changed_at = $3 and sent_at is null`,
		id, toStatus, changedAt, message); err != nil {
		return fmt.Errorf("alerts: mark failed: %w", err)
	}
	return nil
}
