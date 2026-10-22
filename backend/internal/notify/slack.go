package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"downradar.info/backend/internal/store"
)

// slackPayload is one message: the plain sentence a notification shows when the
// blocks cannot be drawn, and the blocks themselves. The sentence says the same
// thing as the blocks, because it is what a phone shows on its lock screen.
type slackPayload struct {
	Text   string       `json:"text"`
	Blocks []slackBlock `json:"blocks"`
}

// slackBlock is one block of a message. Only the kinds this site writes are here:
// a section with text or fields, a context line, and a row of actions.
type slackBlock struct {
	Type     string      `json:"type"`
	Text     *slackText  `json:"text,omitempty"`
	Fields   []slackText `json:"fields,omitempty"`
	Elements []any       `json:"elements,omitempty"`
}

// slackText is a piece of text and how Slack should read it. A plain text is
// shown as written; mrkdwn carries the bold markers and the emoji.
type slackText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

// slackButton is the one action a message offers: the page about the change.
type slackButton struct {
	Type string    `json:"type"`
	Text slackText `json:"text"`
	URL  string    `json:"url"`
}

// slackResult is what one message came back with. An incoming webhook answers a
// 200 with "ok", but a refusal can also be a 200 with the reason in the body, so
// the body is read rather than the status alone.
type slackResult struct {
	OK    bool
	Error string
}

// slackMarks are the status as a Slack message draws it. They are the same
// colours the badges use, so a message and the page it links to agree.
var slackMarks = map[string]string{
	"ok":          ":large_green_circle:",
	"maintenance": ":large_blue_circle:",
	"degraded":    ":large_orange_circle:",
	"partial":     ":large_orange_circle:",
	"major":       ":red_circle:",
	"unknown":     ":white_circle:",
}

// sendSlack posts one delivery to the channel its webhook names.
func (n *Notifier) sendSlack(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.SlackURL) == "" {
		return errors.New("the slack way has no webhook")
	}

	payload, err := n.slackMessage(alert, false)
	if err != nil {
		return err
	}

	result := n.slackCall(ctx, alert.SlackURL, payload)
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestSlack posts the message a delivery would, with a made up change in its
// place, so a channel can be checked before it is relied on. It is the same
// ok-to-major move on the first component the other ways are tried with.
func (n *Notifier) TestSlack(ctx context.Context, serviceID int64, webhookURL string) (slackResult, error) {
	if strings.TrimSpace(webhookURL) == "" {
		return slackResult{Error: "the slack webhook is empty"}, errors.New("the slack webhook is empty")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return slackResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelSlack,
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

	payload, err := n.slackMessage(alert, true)
	if err != nil {
		return slackResult{Error: err.Error()}, err
	}
	return n.slackCall(ctx, webhookURL, payload), nil
}

// WelcomeSlack posts the message a channel gets the moment it is connected, so
// the person sees the install worked. It is not a delivery and carries no way
// out, because nothing has been saved to stop yet.
func (n *Notifier) WelcomeSlack(ctx context.Context, webhookURL, vendorName, vendorSlug string) error {
	if strings.TrimSpace(webhookURL) == "" {
		return errors.New("the slack webhook is empty")
	}

	payload := slackPayload{
		Text: fmt.Sprintf("downradar.info is connected to this channel for %s", vendorName),
		Blocks: []slackBlock{
			{
				Type: "section",
				Text: &slackText{
					Type: "mrkdwn",
					Text: "*downradar.info is connected to this channel*",
				},
			},
			{
				Type: "context",
				Elements: []any{slackText{
					Type: "mrkdwn",
					Text: fmt.Sprintf("Changes to *%s* will arrive here.", slackEscape(vendorName)),
				}},
			},
			{
				Type: "actions",
				Elements: []any{slackButton{
					Type: "button",
					Text: slackText{Type: "plain_text", Text: "Open status page", Emoji: true},
					URL:  fmt.Sprintf("%s/%s", n.publicURL, vendorSlug),
				}},
			},
		},
	}

	result := n.slackCall(ctx, webhookURL, payload)
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// slackMessage is the message one delivery carries. A test says that it is one
// and carries no way out, because nothing has been saved to stop yet.
func (n *Notifier) slackMessage(alert store.PendingAlert, test bool) (slackPayload, error) {
	what := alert.VendorName
	if alert.ComponentName != "" && alert.ComponentName != alert.VendorName {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}

	from := badgeFor(alert.FromStatus)
	to := badgeFor(alert.ToStatus)

	text := fmt.Sprintf("%s is now %s", what, to.Label)
	if test {
		text = "Test: " + text
	}

	payload := slackPayload{
		Text: text,
		Blocks: []slackBlock{
			{
				Type: "section",
				Text: &slackText{Type: "mrkdwn", Text: "*" + slackEscape(text) + "*"},
			},
			{
				Type: "context",
				Elements: []any{slackText{
					Type: "mrkdwn",
					Text: fmt.Sprintf("%s %s until %s",
						slackMark(alert.FromStatus), from.Label,
						alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST")),
				}},
			},
			{
				Type: "actions",
				Elements: []any{slackButton{
					Type: "button",
					Text: slackText{Type: "plain_text", Text: "Open status page", Emoji: true},
					URL:  fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug),
				}},
			},
		},
	}

	if test {
		payload.Blocks = append(payload.Blocks, slackBlock{
			Type:     "context",
			Elements: []any{slackText{Type: "mrkdwn", Text: "This is a test message from downradar.info."}},
		})
	} else if alert.Token != "" {
		payload.Blocks = append(payload.Blocks, slackBlock{
			Type: "context",
			Elements: []any{slackText{
				Type: "mrkdwn",
				Text: fmt.Sprintf("<%s/alerts/unsubscribe?token=%s|Stop these alerts>",
					n.publicURL, alert.Token),
			}},
		})
	}

	return payload, nil
}

// slackCall posts one message and reads what came back.
func (n *Notifier) slackCall(ctx context.Context, webhookURL string, payload slackPayload) slackResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return slackResult{Error: fmt.Sprintf("write the message: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return slackResult{Error: fmt.Sprintf("build the call: %v", err)}
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := n.client.Do(request)
	if err != nil {
		return slackResult{Error: err.Error()}
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	answer := strings.TrimSpace(string(answered))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return slackResult{Error: fmt.Sprintf("slack answered %s: %s", response.Status, answer)}
	}
	if answer == "" {
		return slackResult{Error: "slack answered with nothing"}
	}
	if answer != "ok" {
		return slackResult{Error: fmt.Sprintf("slack answered %q", answer)}
	}
	return slackResult{OK: true}
}

// slackEscape writes what Slack reads as markup as text instead. A name with an
// ampersand or a bracket in it would otherwise change how the message is drawn.
func slackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

// slackMark is the dot a message puts beside a status. A status this build does
// not know is drawn as unknown rather than as nothing.
func slackMark(status string) string {
	if mark, ok := slackMarks[status]; ok {
		return mark
	}
	return ":white_circle:"
}
