package notify

import (
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

// placeholders are the names a webhook's headers can be written with. They are
// what the page offers and what the sender knows how to fill in; a name that is
// not here is left as it was written rather than sent as nothing.
var placeholders = []string{"service", "slug", "component", "from", "to", "at", "url", "text"}

// webhookData is what a webhook's placeholders turn into: the same facts the
// email says, so somebody who reads both is told one story about one change.
type webhookData struct {
	Service   string
	Slug      string
	Component string
	From      string
	To        string
	At        string
	URL       string
	Text      string
}

// webhookPayload is what every call carries in its body. It is not written by
// the subscriber: the fields are the same for every alert, so a receiver can
// read one call and know how to read the next. The service is the vendor as it
// stands now, which is why a receiver can act on the state rather than only on
// the news, and the change that earned the call sits beside it.
type webhookPayload struct {
	Service    webhookService     `json:"service"`
	Changed    webhookChange      `json:"changed"`
	Components []webhookComponent `json:"components"`
}

// webhookService is the vendor the alert is on. Status is written as the site
// stores it, so "major" rather than the wording a page shows.
type webhookService struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

// webhookChange is what happened. Component is empty when the vendor's own
// status is what moved, and the two statuses are the stored names rather than
// the wording. Text is the sentence the email is headed with, for a receiver
// that would rather log a line a person can read.
type webhookChange struct {
	Component string `json:"component"`
	From      string `json:"from"`
	To        string `json:"to"`
	At        string `json:"at"`
	Text      string `json:"text"`
}

// webhookComponent is one thing the vendor reports under itself, with the status
// it is in as this call is made.
type webhookComponent struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Status string `json:"status"`
}

// WebhookResult is what one call came back with. Status is zero when the far end
// was never reached, which is what Error says; Response is as much of what it
// answered as is worth keeping, and is empty when there was nothing to read.
type WebhookResult struct {
	Status   int
	Response string
	Error    string
}

// responseLimit is how much of what a receiver answers is kept. A receiver that
// answers with a novel has said enough after the first page of it, and the rest
// is a table nobody asked to grow.
const responseLimit = 2 << 10

// TestWebhook makes one call the way a delivery would, with a change that never
// happened in place of one that did, so somebody can see their own end answer
// before they rely on it. The status is what came back and the error says whether
// the call was accepted; a far end that refuses is an answer rather than a
// mistake on this side, which is why both are handed back together.
func (n *Notifier) TestWebhook(ctx context.Context, serviceID int64, hook store.AlertWebhook) (WebhookResult, error) {
	if hook.URL == "" {
		return WebhookResult{Error: "the webhook has no URL"}, errors.New("the webhook has no URL")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return WebhookResult{Error: err.Error()}, err
	}

	// Nothing has moved, so the vendor's first component is named as though it
	// had: a receiver that routes on a `{{component}}` header sees one filled in
	// rather than an empty string, and a vendor with no components at all is
	// tried as its own status instead.
	alert := store.PendingAlert{
		Kind:       store.ChannelWebhook,
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

	data := n.webhookData(alert)
	body, err := webhookBody(alert, data, snapshot)
	if err != nil {
		return WebhookResult{Error: err.Error()}, err
	}
	return n.webhookCall(ctx, hook, body, data)
}

// sendWebhook makes one call for a delivery.
func (n *Notifier) sendWebhook(ctx context.Context, alert store.PendingAlert) error {
	hook := alert.Webhook
	if hook.URL == "" {
		return errors.New("the webhook has no URL")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, alert.ServiceID)
	if err != nil {
		return err
	}

	data := n.webhookData(alert)
	body, err := webhookBody(alert, data, snapshot)
	if err != nil {
		return err
	}

	result, err := n.webhookCall(ctx, hook, body, data)
	n.recordCall(ctx, alert.UserID, alert.ServiceID, &alert.ChannelID, store.CallDelivery, hook, result)
	return err
}

// webhookCall makes one call and reads what came back, without deciding what a
// status means: a delivery treats anything but a 2xx as a refusal, while a try
// from the page is there to show the subscriber exactly what their end said. A
// receiver that answers anything but a 2xx has refused it, and one that cannot be
// reached is a failure as well.
func (n *Notifier) webhookCall(ctx context.Context, hook store.AlertWebhook, body string, data webhookData) (WebhookResult, error) {
	headers, err := renderHeaders(hook.Headers, data)
	if err != nil {
		return WebhookResult{Error: err.Error()}, err
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, webhookMethod(hook), hook.URL, strings.NewReader(body))
	if err != nil {
		failure := fmt.Errorf("build the call: %w", err)
		return WebhookResult{Error: failure.Error()}, failure
	}
	for _, header := range headers {
		request.Header.Set(header.Name, header.Value)
	}
	// What the body is says what the call carries, unless the subscriber has
	// said it themselves.
	if request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := n.client.Do(request)
	if err != nil {
		failure := fmt.Errorf("call %s: %w", hook.URL, err)
		return WebhookResult{Error: err.Error()}, failure
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	result := WebhookResult{Status: response.StatusCode, Response: string(answered)}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		result.Error = fmt.Sprintf("the webhook answered %s", response.Status)
		return result, errors.New(result.Error)
	}
	return result, nil
}

// recordCall writes one call down where the page can show it. A failure to write
// it is logged rather than returned: whether the call itself arrived is what the
// delivery is judged on, and the log is there to be read afterwards.
func (n *Notifier) recordCall(ctx context.Context, userID *int64, serviceID int64, channelID *int64, kind string, hook store.AlertWebhook, result WebhookResult) {
	err := n.store.LogWebhookCall(ctx, userID, serviceID, channelID, store.WebhookCall{
		Kind:     kind,
		URL:      hook.URL,
		Method:   webhookMethod(hook),
		Status:   statusOrNil(result.Status),
		Response: result.Response,
		Error:    result.Error,
	})
	if err != nil {
		n.log.Error("failed to record a webhook call", "kind", kind, "error", err)
	}
}

// webhookMethod is the method a call goes out with. A webhook saved before the
// method was asked for has none, and POST is what it was sent with.
func webhookMethod(hook store.AlertWebhook) string {
	if hook.Method == "" {
		return http.MethodPost
	}
	return hook.Method
}

// statusOrNil turns "no answer" into a null rather than a zero, so a call that was
// never answered is not shown as a call that got a 0.
func statusOrNil(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}

// webhookData is what the placeholders in one delivery turn into.
func (n *Notifier) webhookData(alert store.PendingAlert) webhookData {
	what := alert.VendorName
	if alert.ComponentName != "" {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}
	to := badgeFor(alert.ToStatus).Label

	return webhookData{
		Service:   alert.VendorName,
		Slug:      alert.VendorSlug,
		Component: alert.ComponentName,
		From:      badgeFor(alert.FromStatus).Label,
		To:        to,
		At:        alert.ChangedAt.UTC().Format(time.RFC3339),
		URL:       fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug),
		Text:      fmt.Sprintf("%s is now %s", what, to),
	}
}

// webhookBody writes what a call carries. The subscriber's own words go in the
// headers; here the statuses are written as the site stores them, because a
// receiver matches on those rather than on the wording a page shows. The values
// are put in by the encoder rather than into a template, so a name with a quote
// in it cannot end the string it sits in.
func webhookBody(alert store.PendingAlert, data webhookData, snapshot store.ServiceSnapshot) (string, error) {
	payload := webhookPayload{
		Service: webhookService{
			Name:   alert.VendorName,
			Slug:   alert.VendorSlug,
			URL:    data.URL,
			Status: snapshot.Status,
		},
		Changed: webhookChange{
			Component: alert.ComponentName,
			From:      alert.FromStatus,
			To:        alert.ToStatus,
			At:        data.At,
			Text:      data.Text,
		},
		Components: make([]webhookComponent, 0, len(snapshot.Components)),
	}
	for _, component := range snapshot.Components {
		payload.Components = append(payload.Components, webhookComponent{
			Name:   component.Name,
			Group:  component.Group,
			Status: component.Status,
		})
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("write the body: %w", err)
	}
	return string(encoded), nil
}

// renderHeaders fills the placeholders in the headers the subscriber wrote. A
// header cannot hold a line break, so one that does is refused rather than sent:
// that would be a way to write a header this call did not ask for.
func renderHeaders(headers []store.AlertField, data webhookData) ([]store.AlertField, error) {
	out := make([]store.AlertField, 0, len(headers))
	for _, header := range headers {
		name := fill(header.Name, data)
		value := fill(header.Value, data)
		if strings.ContainsAny(name+value, "\r\n") {
			return nil, fmt.Errorf("the header %q cannot hold a line break", name)
		}
		out = append(out, store.AlertField{Name: name, Value: value})
	}
	return out, nil
}

// fill replaces the placeholders in one value.
func fill(template string, data webhookData) string {
	replacements := make([]string, 0, len(placeholders)*2)
	for _, name := range placeholders {
		replacements = append(replacements, "{{"+name+"}}", value(data, name))
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

// value reads one placeholder out of what a change says.
func value(data webhookData, name string) string {
	switch name {
	case "service":
		return data.Service
	case "slug":
		return data.Slug
	case "component":
		return data.Component
	case "from":
		return data.From
	case "to":
		return data.To
	case "at":
		return data.At
	case "url":
		return data.URL
	case "text":
		return data.Text
	default:
		return ""
	}
}
