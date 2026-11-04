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

// A Teams message is an Adaptive Card wrapped in the envelope a Workflows
// webhook expects. The card is what the channel sees; the envelope is how Teams
// knows which card to render.
type teamsPayload struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Attachments []teamsAttachment `json:"attachments"`
}

type teamsAttachment struct {
	ContentType string    `json:"contentType"`
	Content     teamsCard `json:"content"`
}

type teamsCard struct {
	Schema  string        `json:"$schema"`
	Type    string        `json:"type"`
	Version string        `json:"version"`
	Body    []any         `json:"body"`
	Actions []teamsAction `json:"actions,omitempty"`
}

type teamsText struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Weight string `json:"weight,omitempty"`
	Size   string `json:"size,omitempty"`
	Color  string `json:"color,omitempty"`
	Wrap   bool   `json:"wrap,omitempty"`
}

type teamsFactSet struct {
	Type  string      `json:"type"`
	Facts []teamsFact `json:"facts"`
}

type teamsFact struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

type teamsAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// teamsResult is what the workflow answered. A workflow takes a request with an
// empty body, so only a refusal has anything worth showing.
type teamsResult struct {
	OK    bool
	Error string
}

// teamsColors draw the status in the card's own palette.
var teamsColors = map[string]string{
	"ok":          "Good",
	"maintenance": "Warning",
	"degraded":    "Warning",
	"partial":     "Warning",
	"major":       "Attention",
	"unknown":     "Default",
}

// sendTeams posts one delivery to the channel or chat its Workflows webhook
// names.
func (n *Notifier) sendTeams(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.TeamsURL) == "" {
		return errors.New("the Teams way has no webhook")
	}

	result := n.teamsCall(ctx, alert.TeamsURL, n.teamsMessage(alert, false))
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestTeams posts a made up change so a channel can be checked before it is
// relied on, the same way the other ways are tried.
func (n *Notifier) TestTeams(ctx context.Context, serviceID int64, webhookURL string) (teamsResult, error) {
	if strings.TrimSpace(webhookURL) == "" {
		return teamsResult{Error: "the Teams webhook is empty"}, errors.New("the Teams webhook is empty")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return teamsResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelTeams,
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

	return n.teamsCall(ctx, webhookURL, n.teamsMessage(alert, true)), nil
}

// teamsMessage writes the card one delivery carries. The title names the change,
// the facts say what moved, and the actions open the vendor page or stop the
// alert.
func (n *Notifier) teamsMessage(alert store.PendingAlert, test bool) teamsPayload {
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

	body := []any{
		teamsText{
			Type:   "TextBlock",
			Text:   title,
			Weight: "Bolder",
			Size:   "Medium",
			Color:  teamsColor(alert.ToStatus),
			Wrap:   true,
		},
		teamsText{
			Type: "TextBlock",
			Text: fmt.Sprintf("It was %s until %s.",
				from.Label, alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST")),
			Wrap: true,
		},
	}

	if test {
		body = append([]any{teamsText{
			Type: "TextBlock",
			Text: "This is a test message from downradar.info.",
			Wrap: true,
		}}, body...)
	}

	facts := []teamsFact{{Title: "Service", Value: alert.VendorName}}
	if alert.ComponentName != "" && alert.ComponentName != alert.VendorName {
		facts = append(facts, teamsFact{Title: "Component", Value: alert.ComponentName})
	}
	facts = append(facts, teamsFact{Title: "Status", Value: to.Label})
	body = append(body, teamsFactSet{Type: "FactSet", Facts: facts})

	page := fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug)
	card := teamsCard{
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Type:    "AdaptiveCard",
		Version: "1.2",
		Body:    body,
		Actions: []teamsAction{{
			Type:  "Action.OpenUrl",
			Title: "Open status page",
			URL:   page,
		}},
	}
	if !test && alert.Token != "" {
		card.Actions = append(card.Actions, teamsAction{
			Type:  "Action.OpenUrl",
			Title: "Stop these alerts",
			URL:   fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token),
		})
	}

	return teamsPayload{
		Type: "message",
		Text: fmt.Sprintf("%s. It was %s until %s.",
			title, from.Label, alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST")),
		Attachments: []teamsAttachment{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			Content:     card,
		}},
	}
}

// teamsCall posts one card. A workflow answers with an empty 2xx when it took it;
// anything else says why in the body.
func (n *Notifier) teamsCall(ctx context.Context, webhookURL string, payload teamsPayload) teamsResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return teamsResult{Error: fmt.Sprintf("write the message: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return teamsResult{Error: fmt.Sprintf("build the call: %v", err)}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", n.userAgent())

	response, err := n.client.Do(request)
	if err != nil {
		return teamsResult{Error: err.Error()}
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	answer := strings.TrimSpace(string(answered))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return teamsResult{Error: fmt.Sprintf("Teams answered %s: %s", response.Status, answer)}
	}
	return teamsResult{OK: true}
}

func teamsColor(status string) string {
	if color, ok := teamsColors[status]; ok {
		return color
	}
	return "Default"
}
