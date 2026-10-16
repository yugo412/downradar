// Package notify sends the emails a status change earns. The change itself
// writes its deliveries as part of the same transaction that records it, and
// this is the loop that drains them: polling never waits for a mail server, and
// a delivery that fails is retried a few times before it is left with its error.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
)

type Notifier struct {
	store     *store.Store
	sender    Sender
	publicURL string
	interval  time.Duration
	log       *slog.Logger
}

func New(st *store.Store, sender Sender, publicURL string, interval time.Duration, log *slog.Logger) *Notifier {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Notifier{
		store:     st,
		sender:    sender,
		publicURL: strings.TrimSuffix(publicURL, "/"),
		interval:  interval,
		log:       log,
	}
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
		subject, body := n.message(alert)

		if err := n.sender.Send(ctx, alert.Email, subject, body); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n.log.Warn("alert delivery failed",
				"vendor", alert.VendorSlug, "attempt", alert.Attempts+1, "error", err)
			if recErr := n.store.FailedAlert(ctx, alert.ID, err.Error()); recErr != nil {
				n.log.Error("failed to record the alert failure", "id", alert.ID, "error", recErr)
			}
			continue
		}

		if err := n.store.SentAlert(ctx, alert.ID); err != nil {
			n.log.Error("failed to mark the alert sent", "id", alert.ID, "error", err)
			continue
		}
		n.log.Info("alert sent", "vendor", alert.VendorSlug, "status", alert.ToStatus)
	}

	return nil
}

// message is what the subscriber reads: what changed, when, where to look, and
// the way out.
func (n *Notifier) message(alert store.PendingAlert) (string, string) {
	subject := fmt.Sprintf("%s is now %s", alert.VendorName, label(alert.ToStatus))

	body := fmt.Sprintf(`%s changed status at %s.

%s -> %s

Live status and history: %s/%s

You are getting this email because you asked for alerts about %s.
Unsubscribe: %s/alerts/unsubscribe?token=%s
`,
		alert.VendorName,
		alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST"),
		label(alert.FromStatus),
		label(alert.ToStatus),
		n.publicURL, alert.VendorSlug,
		alert.VendorName,
		n.publicURL, alert.Token,
	)

	return subject, body
}

// label is the wording the site uses for a status, so an email and the page a
// subscriber opens next agree with each other.
var labels = map[string]string{
	"ok":          "Operational",
	"maintenance": "Maintenance",
	"degraded":    "Degraded",
	"partial":     "Partial outage",
	"major":       "Major outage",
	"unknown":     "Unknown",
}

func label(status string) string {
	if text, ok := labels[status]; ok {
		return text
	}
	return status
}
