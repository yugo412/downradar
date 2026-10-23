package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// webhookPayload is what every call carries in its body. The fields are not the
// subscriber's, so a receiver can read one call and know the next; the service is
// the vendor as it stands now, and the change that earned the call sits beside it.
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

// webhookChange is what happened. Component is empty when the vendor's own status
// moved, and the statuses are the stored names. Text is the readable sentence a
// receiver can log instead.
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

// TestWebhook makes one call the way a delivery would, with a made up change in
// place of a real one, so somebody can see their own end answer before relying on
// it. A refusal from the far end is an answer, so it comes back with the result.
func (n *Notifier) TestWebhook(ctx context.Context, serviceID int64, hook store.AlertWebhook) (WebhookResult, error) {
	if hook.URL == "" {
		return WebhookResult{Error: "the webhook has no URL"}, errors.New("the webhook has no URL")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return WebhookResult{Error: err.Error()}, err
	}

	// Nothing has moved, so the first component is named as though it had: a
	// receiver routing on a `{{component}}` header gets one filled in, and a
	// vendor with no components is tried as its own status instead.
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
	return n.webhookCall(ctx, hook, body, data, webhookMessageID(alert))
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

	result, err := n.webhookCall(ctx, hook, body, data, webhookMessageID(alert))
	n.recordCall(ctx, alert.UserID, alert.ServiceID, &alert.ChannelID, store.CallDelivery, hook, result)
	return err
}

// webhookCall makes one call and reads what came back, without deciding what a
// status means: a delivery treats anything but a 2xx as a refusal, while a try
// from the page shows the subscriber exactly what their end said.
func (n *Notifier) webhookCall(ctx context.Context, hook store.AlertWebhook, body string, data webhookData, messageID string) (WebhookResult, error) {
	headers, err := renderHeaders(hook.Headers, data)
	if err != nil {
		return WebhookResult{Error: err.Error()}, err
	}

	// A signed call carries the signature beside whatever the subscriber wrote.
	// The three header names are the sender's, so one written by hand is
	// replaced rather than sent twice.
	if hook.Auth == store.WebhookAuthStandard && hook.Signing.Secret != "" {
		signed, err := standardHeaders(hook, messageID, body, time.Now())
		if err != nil {
			return WebhookResult{Error: err.Error()}, err
		}
		headers = replaceHeaders(headers, signed)
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
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", n.userAgent())
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
// headers; here the statuses are the stored names, because a receiver matches on
// those. The encoder writes the values, so a quote in a name cannot break out.
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

// webhookMessageID is the webhook-id a call carries. A delivery keeps the id of
// its row across attempts, so a receiver that has seen one can ignore the repeat
// rather than act twice; a try from the page has no row and gets a fresh one.
func webhookMessageID(alert store.PendingAlert) string {
	if alert.ID > 0 {
		return "msg_" + strconv.FormatInt(alert.ID, 10)
	}

	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "msg_" + base64.RawURLEncoding.EncodeToString(buf)
}

// standardHeaders are the three headers the Standard Webhooks specification puts
// on a signed call: the message id, when it was made, and the signature over both
// and the body. A rotation going out is a second signature in the same header.
func standardHeaders(hook store.AlertWebhook, id, body string, now time.Time) ([]store.AlertField, error) {
	key, err := store.WebhookSigningKey(hook.Signing.Secret)
	if err != nil {
		return nil, err
	}

	timestamp := strconv.FormatInt(now.Unix(), 10)
	signed := id + "." + timestamp + "." + body
	signatures := []string{"v1," + signCall(key, signed)}

	if hook.Signing.Previous != "" && now.Before(hook.Signing.PreviousUntil) {
		if retired, err := store.WebhookSigningKey(hook.Signing.Previous); err == nil {
			signatures = append(signatures, "v1,"+signCall(retired, signed))
		}
	}

	return []store.AlertField{
		{Name: "webhook-id", Value: id},
		{Name: "webhook-timestamp", Value: timestamp},
		{Name: "webhook-signature", Value: strings.Join(signatures, " ")},
	}, nil
}

// signCall is one signature over what the specification says is signed.
func signCall(key []byte, signed string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signed))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// replaceHeaders lays the sender's own headers over the subscriber's, dropping a
// name that is written in both rather than sending it twice.
func replaceHeaders(headers, replace []store.AlertField) []store.AlertField {
	out := make([]store.AlertField, 0, len(headers)+len(replace))
	for _, header := range headers {
		taken := false
		for _, other := range replace {
			if strings.EqualFold(strings.TrimSpace(header.Name), other.Name) {
				taken = true
				break
			}
		}
		if !taken {
			out = append(out, header)
		}
	}
	return append(out, replace...)
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
