package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"downradar.info/backend/internal/store"
)

// ntfyMessage is one notification: the title a phone shows, the body under it,
// and the two links ntfy can put behind a tap and a button. The title is a header
// rather than part of the body, so a phone can show it without the rest.
type ntfyMessage struct {
	Title   string
	Body    string
	Click   string
	Actions string
}

// ntfyResult is what one publish came back with. A successful publish answers
// with plain text, so only a refusal carries an error worth showing.
type ntfyResult struct {
	OK    bool
	Error string
}

// sendNtfy publishes one delivery to the topic its URL names.
func (n *Notifier) sendNtfy(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.NtfyURL) == "" {
		return errors.New("the ntfy way has no topic")
	}

	result := n.ntfyCall(ctx, alert.NtfyURL, alert.NtfyToken, n.ntfyMessage(alert, false))
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestNtfy publishes the message a delivery would, with a made up change in its
// place, so a topic can be checked before it is relied on. It is the same
// ok-to-major move the other ways are tried with.
func (n *Notifier) TestNtfy(ctx context.Context, serviceID int64, topicURL, token string) (ntfyResult, error) {
	if strings.TrimSpace(topicURL) == "" {
		return ntfyResult{Error: "the ntfy topic is empty"}, errors.New("the ntfy topic is empty")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return ntfyResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelNtfy,
		ServiceID:  serviceID,
		VendorName: snapshot.Name,
		VendorSlug: snapshot.Slug,
		FromStatus: "ok",
		ToStatus:   "major",
		ChangedAt:  time.Now(),
	}
	if len(snapshot.Components) > 0 {
		alert.ComponentName = snapshot.Components[0].Name
	}

	return n.ntfyCall(ctx, topicURL, token, n.ntfyMessage(alert, true)), nil
}

// ntfyMessage writes the notification one delivery carries. A test says that it
// is one and carries no way out, because nothing has been saved to stop yet.
func (n *Notifier) ntfyMessage(alert store.PendingAlert, test bool) ntfyMessage {
	what := alert.VendorName
	if alert.ComponentName != "" {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}

	title := fmt.Sprintf("%s is now %s", what, badgeFor(alert.ToStatus).Label)
	if test {
		title = "Test: " + title
	}

	url := fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug)
	body := []string{}
	if test {
		body = append(body, "This is a test message from DownRadar.")
	}
	body = append(body,
		fmt.Sprintf("From %s to %s at %s.", badgeFor(alert.FromStatus).Label,
			badgeFor(alert.ToStatus).Label, alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST")),
	)

	actions := fmt.Sprintf("view, Open status page, %s", url)
	if !test {
		unsubscribe := fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token)
		body = append(body, "", "Stop these alerts: "+unsubscribe)
		actions += fmt.Sprintf("; view, Stop alerts, %s", unsubscribe)
	}

	return ntfyMessage{
		Title:   title,
		Body:    strings.Join(body, "\n"),
		Click:   url,
		Actions: actions,
	}
}

// ntfyCall publishes one message. The topic URL carries the topic, so the body
// is the message itself; the access token travels as a bearer token and is never
// part of the URL. A refusal is read back so the page can say what the server
// answered.
func (n *Notifier) ntfyCall(ctx context.Context, topicURL, token string, message ntfyMessage) ntfyResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, topicURL, strings.NewReader(message.Body))
	if err != nil {
		return ntfyResult{Error: fmt.Sprintf("prepare the ntfy call: %v", err)}
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", n.userAgent())
	if message.Title != "" {
		req.Header.Set("Title", message.Title)
	}
	if message.Click != "" {
		req.Header.Set("Click", message.Click)
	}
	if message.Actions != "" {
		req.Header.Set("Actions", message.Actions)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return ntfyResult{Error: fmt.Sprintf("call the ntfy topic: %v", err)}
	}
	defer resp.Body.Close()

	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(answer))
		if text == "" {
			return ntfyResult{Error: fmt.Sprintf("ntfy answered %s", resp.Status)}
		}
		return ntfyResult{Error: fmt.Sprintf("ntfy answered %s: %s", resp.Status, text)}
	}
	return ntfyResult{OK: true}
}
