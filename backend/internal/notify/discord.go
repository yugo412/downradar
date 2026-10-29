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

// discordPayload is one message: an embed and, for a delivery, a row of buttons.
type discordPayload struct {
	Embeds     []discordEmbed     `json:"embeds"`
	Components []discordActionRow `json:"components,omitempty"`
}

// discordEmbed is the post. The title carries the change; the colour is the
// status it moved to.
type discordEmbed struct {
	Title       string         `json:"title,omitempty"`
	URL         string         `json:"url,omitempty"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
}

type discordFooter struct {
	Text string `json:"text"`
}

// discordActionRow holds the buttons: type 1 is an action row, and a link button
// is type 2 style 5.
type discordActionRow struct {
	Type       int             `json:"type"`
	Components []discordButton `json:"components"`
}

type discordButton struct {
	Type  int    `json:"type"`
	Style int    `json:"style"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// discordResult is what Discord answered: 204 with no body for a message it took,
// and a status outside 2xx with the reason in the body for a refusal.
type discordResult struct {
	OK    bool
	Error string
}

// discordColors colour a status the way the badges do.
var discordColors = map[string]int{
	"ok":          0x007f43,
	"maintenance": 0x4ba8c0,
	"degraded":    0xee8134,
	"partial":     0xee8134,
	"major":       0xe93f33,
	"unknown":     0xeeeeee,
}

// sendDiscord posts one delivery to the channel its webhook names.
func (n *Notifier) sendDiscord(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.DiscordURL) == "" {
		return errors.New("the discord way has no webhook")
	}

	result := n.discordCall(ctx, alert.DiscordURL, n.discordMessage(alert, false))
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestDiscord posts a made up change so a channel can be checked before it is
// relied on, the same way the other ways are tried.
func (n *Notifier) TestDiscord(ctx context.Context, serviceID int64, webhookURL string) (discordResult, error) {
	if strings.TrimSpace(webhookURL) == "" {
		return discordResult{Error: "the discord webhook is empty"}, errors.New("the discord webhook is empty")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return discordResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelDiscord,
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

	return n.discordCall(ctx, webhookURL, n.discordMessage(alert, true)), nil
}

// WelcomeDiscord posts the message a channel gets the moment it is connected,
// so the person sees the install worked. It is not a delivery and carries no way
// out, because nothing has been saved to stop yet.
func (n *Notifier) WelcomeDiscord(ctx context.Context, webhookURL, vendorName, vendorSlug string) error {
	if strings.TrimSpace(webhookURL) == "" {
		return errors.New("the discord webhook is empty")
	}

	page := fmt.Sprintf("%s/%s", n.publicURL, vendorSlug)
	payload := discordPayload{
		Embeds: []discordEmbed{{
			Title:       "downradar.info is connected to this channel",
			URL:         page,
			Description: fmt.Sprintf("Changes to %s will arrive here.", vendorName),
			Color:       discordColor("ok"),
			Footer:      &discordFooter{Text: "downradar.info"},
		}},
		Components: []discordActionRow{{
			Type: 1,
			Components: []discordButton{{
				Type:  2,
				Style: 5,
				Label: "Open status page",
				URL:   page,
			}},
		}},
	}

	result := n.discordCall(ctx, webhookURL, payload)
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// discordMessage writes the post. A test says so and carries no way out.
func (n *Notifier) discordMessage(alert store.PendingAlert, test bool) discordPayload {
	what := alert.VendorName
	if alert.ComponentName != "" && alert.ComponentName != alert.VendorName {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}

	from := badgeFor(alert.FromStatus)
	to := badgeFor(alert.ToStatus)

	title := fmt.Sprintf("%s is now %s", what, to.Label)
	if test {
		title = "Test: " + title
	}

	description := fmt.Sprintf("It was %s until %s.",
		from.Label, alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST"))
	if test {
		description = "This is a test message from downradar.info.\n\n" + description
	}

	page := fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug)
	payload := discordPayload{
		Embeds: []discordEmbed{{
			Title:       title,
			URL:         page,
			Description: description,
			Color:       discordColor(alert.ToStatus),
			Timestamp:   alert.ChangedAt.UTC().Format(time.RFC3339),
			Footer:      &discordFooter{Text: "downradar.info"},
		}},
		Components: []discordActionRow{{
			Type: 1,
			Components: []discordButton{{
				Type:  2,
				Style: 5,
				Label: "Open status page",
				URL:   page,
			}},
		}},
	}

	if !test && alert.Token != "" {
		payload.Components[0].Components = append(payload.Components[0].Components, discordButton{
			Type:  2,
			Style: 5,
			Label: "Stop these alerts",
			URL:   fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token),
		})
	}

	return payload
}

// discordCall posts one message. An empty body with a 2xx is the normal answer;
// anything else says why in the body.
func (n *Notifier) discordCall(ctx context.Context, webhookURL string, payload discordPayload) discordResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return discordResult{Error: fmt.Sprintf("write the message: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return discordResult{Error: fmt.Sprintf("build the call: %v", err)}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", n.userAgent())

	response, err := n.client.Do(request)
	if err != nil {
		return discordResult{Error: err.Error()}
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	answer := strings.TrimSpace(string(answered))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return discordResult{Error: fmt.Sprintf("discord answered %s: %s", response.Status, answer)}
	}
	return discordResult{OK: true}
}

// discordColor is the colour a status is drawn in; anything unknown is grey.
func discordColor(status string) int {
	if color, ok := discordColors[status]; ok {
		return color
	}
	return 0xeeeeee
}
