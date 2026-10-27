// Package notify sends what a status change earns: an email, a Telegram, Slack
// or Discord message, or a webhook call. The change writes its deliveries in the
// same transaction that records it; this loop drains them and retries a failure.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"downradar.info/backend/internal/mail"
	"downradar.info/backend/internal/store"
)

// Sender delivers one message. The SMTP mailer is the real one; tests pass a
// fake, so the loop can be exercised without a server.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

const (
	batchSize   = 50
	maxAttempts = 3

	// webhookTimeout is how long one call may take before it counts as failed: a
	// receiver that has not answered by then is not going to, and the delivery is
	// better off recorded than stuck.
	webhookTimeout = 10 * time.Second
)

type Notifier struct {
	store     *store.Store
	sender    Sender
	client    *http.Client
	publicURL string
	interval  time.Duration
	log       *slog.Logger

	// telegramAPI is where the Bot API lives, which a self-hosted bot can move.
	// The bot itself belongs to the subscriber, so its token travels with each
	// channel rather than with the sender.
	telegramAPI string
}

func New(st *store.Store, sender Sender, publicURL string, interval time.Duration, log *slog.Logger) *Notifier {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Notifier{
		store:     st,
		sender:    sender,
		client:    &http.Client{Timeout: webhookTimeout},
		publicURL: strings.TrimSuffix(publicURL, "/"),
		interval:  interval,
		log:       log,
	}
}

// WithTelegram tells the notifier where the Bot API lives. The bot tokens
// themselves travel with the channels, since each subscriber brings their own.
func (n *Notifier) WithTelegram(apiBase string) *Notifier {
	n.telegramAPI = strings.TrimSpace(apiBase)
	return n
}

// userAgent names this sender on an outbound call, so a receiver sees the site
// rather than the Go client's default. A webhook that sets its own User-Agent
// keeps it.
func (n *Notifier) userAgent() string {
	if n.publicURL == "" {
		return "DownRadar"
	}
	return "DownRadar (+" + n.publicURL + ")"
}

func (n *Notifier) Run(ctx context.Context) {
	n.log.Info("alert sender started", "interval", n.interval, "public_url", n.publicURL)

	for {
		if err := n.drain(ctx); err != nil && ctx.Err() == nil {
			n.log.Error("alert delivery pass failed", "error", err)
		}

		select {
		case <-ctx.Done():
			n.log.Info("alert sender stopped")
			return
		case <-time.After(n.interval):
		}
	}
}

func (n *Notifier) drain(ctx context.Context) error {
	pending, err := n.store.PendingAlerts(ctx, maxAttempts, batchSize)
	if err != nil {
		return err
	}

	for _, alert := range pending {
		var err error
		switch alert.Kind {
		case store.ChannelWebhook:
			err = n.sendWebhook(ctx, alert)
		case store.ChannelTelegram:
			err = n.sendTelegram(ctx, alert)
		case store.ChannelSlack:
			err = n.sendSlack(ctx, alert)
		case store.ChannelDiscord:
			err = n.sendDiscord(ctx, alert)
		default:
			err = n.sendEmail(ctx, alert)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n.log.Warn("alert delivery failed", "kind", alert.Kind, "vendor", alert.VendorSlug,
				"attempt", alert.Attempts+1, "error", err)
			if recErr := n.store.FailedAlert(ctx, alert.ID, alert.ToStatus, alert.ChangedAt, err.Error()); recErr != nil {
				n.log.Error("failed to record the alert failure", "id", alert.ID, "error", recErr)
			}
			continue
		}

		if err := n.store.SentAlert(ctx, alert.ID, alert.ToStatus, alert.ChangedAt); err != nil {
			n.log.Error("failed to mark the alert sent", "id", alert.ID, "error", err)
			continue
		}
		n.log.Info("alert sent", "kind", alert.Kind, "vendor", alert.VendorSlug, "status", alert.ToStatus)
	}

	return nil
}

// sendEmail writes the message and hands it to the mailer. A message that cannot
// be written at all is a mistake in the code, since the templates are embedded,
// and the delivery is left with the error rather than sent as an empty email.
func (n *Notifier) sendEmail(ctx context.Context, alert store.PendingAlert) error {
	subject, body, err := n.message(alert)
	if err != nil {
		return fmt.Errorf("write the message: %w", err)
	}
	return n.sender.Send(ctx, alert.Email, subject, body)
}

// message is what the subscriber reads: what changed, when, where to look, and
// the way out. A subscriber who picked components is told which one moved, not
// just the vendor, and the wording itself lives in the email template.
func (n *Notifier) message(alert store.PendingAlert) (string, string, error) {
	what := alert.VendorName
	if alert.ComponentName != "" {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}

	subject := fmt.Sprintf("%s is now %s", what, badgeFor(alert.ToStatus).Label)

	// The summary is reused as the opening line only when it was written after
	// this change. An older sentence describes a state the reader has left.
	summary := ""
	if alert.GeneratedSummary != "" && alert.SummaryGeneratedAt != nil &&
		!alert.SummaryGeneratedAt.Before(alert.ChangedAt) {
		summary = alert.GeneratedSummary
	}

	body, err := mail.Render("alert", alertData{
		VendorName:    alert.VendorName,
		ComponentName: alert.ComponentName,
		Summary:       summary,
		What:          what,
		From:          badgeFor(alert.FromStatus),
		To:            badgeFor(alert.ToStatus),
		ChangedAt:     alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST"),
		URL:           fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug),
		Unsubscribe:   fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token),
	})
	if err != nil {
		return "", "", err
	}
	return subject, body, nil
}

// alertData is what the alert template is written with. What and the two names
// overlap on purpose: the heading is the vendor and the component under it, while
// the closing line names the thing the subscriber asked about.
type alertData struct {
	VendorName    string
	ComponentName string
	Summary       string
	What          string
	From          badge
	To            badge
	ChangedAt     string
	URL           string
	Unsubscribe   string
}

// badge is a status as the email draws it: the word the site uses for it, and the
// colours its badge is filled with. They are the theme's state colours, so an
// email and the page it links to agree about what "Degraded" looks like.
type badge struct {
	Label string
	Color string
	Ink   string
}

var badges = map[string]badge{
	"ok":          {Label: "Operational", Color: "#007f43", Ink: "#ffffff"},
	"maintenance": {Label: "Maintenance", Color: "#4ba8c0", Ink: "#ffffff"},
	"degraded":    {Label: "Degraded", Color: "#ee8134", Ink: "#ffffff"},
	"partial":     {Label: "Partial outage", Color: "#ee8134", Ink: "#ffffff"},
	"major":       {Label: "Major outage", Color: "#e93f33", Ink: "#ffffff"},
	"unknown":     {Label: "Unknown", Color: "#eeeeee", Ink: "#161616"},
}

// badgeFor is the wording the site uses for a status, so an email and the page a
// subscriber opens next agree with each other. A status this build does not know
// is shown as it came rather than as nothing.
func badgeFor(status string) badge {
	if b, ok := badges[status]; ok {
		return b
	}
	return badge{Label: status, Color: "#eeeeee", Ink: "#161616"}
}
