package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
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

// larkPayload is one custom bot request. When the group requires a signature,
// Timestamp and Sign ride beside the message.
type larkPayload struct {
	Timestamp string   `json:"timestamp,omitempty"`
	Sign      string   `json:"sign,omitempty"`
	MsgType   string   `json:"msg_type"`
	Card      larkCard `json:"card"`
}

type larkCard struct {
	Config   larkConfig `json:"config"`
	Header   larkHeader `json:"header"`
	Elements []any      `json:"elements"`
}

type larkConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

type larkHeader struct {
	Template string   `json:"template"`
	Title    larkText `json:"title"`
}

type larkText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

type larkDiv struct {
	Tag  string   `json:"tag"`
	Text larkText `json:"text"`
}

type larkAction struct {
	Tag     string       `json:"tag"`
	Actions []larkButton `json:"actions"`
}

type larkButton struct {
	Tag  string   `json:"tag"`
	Text larkText `json:"text"`
	URL  string   `json:"url"`
	Type string   `json:"type"`
}

// larkResult is what Lark answered. A custom bot reports a refusal in the body
// with code and msg, so the HTTP status alone is not enough.
type larkResult struct {
	OK      bool
	Code    int
	Message string
	Error   string
}

var larkTemplates = map[string]string{
	"ok":          "green",
	"maintenance": "blue",
	"degraded":    "orange",
	"partial":     "orange",
	"major":       "red",
	"unknown":     "grey",
}

// sendLark posts one delivery to the group its custom bot webhook names.
func (n *Notifier) sendLark(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.LarkURL) == "" {
		return errors.New("the lark way has no webhook")
	}

	result := n.larkCall(ctx, alert.LarkURL, alert.LarkSecret, n.larkMessage(alert, false))
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestLark posts a made up change so a group can be checked before it is relied
// on. The answer is Lark's own, including its code when it refuses.
func (n *Notifier) TestLark(ctx context.Context, serviceID int64, webhookURL, secret string) (larkResult, error) {
	if strings.TrimSpace(webhookURL) == "" {
		return larkResult{Error: "the lark webhook is empty"}, errors.New("the lark webhook is empty")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return larkResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelLark,
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

	return n.larkCall(ctx, webhookURL, secret, n.larkMessage(alert, true)), nil
}

// larkMessage writes the card one delivery carries. The status picks the header
// colour; the card names the change and links to the vendor's page.
func (n *Notifier) larkMessage(alert store.PendingAlert, test bool) larkPayload {
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

	details := fmt.Sprintf("From: %s\nTo: %s\nChanged: %s",
		from.Label, to.Label, alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST"))
	if test {
		details = "This is a test message from downradar.info.\n\n" + details
	}

	page := fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug)
	elements := []any{
		larkDiv{
			Tag:  "div",
			Text: larkText{Tag: "plain_text", Content: details},
		},
		larkAction{
			Tag: "action",
			Actions: []larkButton{{
				Tag:  "button",
				Text: larkText{Tag: "plain_text", Content: "Open status page"},
				URL:  page,
				Type: "primary",
			}},
		},
	}

	if !test && alert.Token != "" {
		elements = append(elements, larkAction{
			Tag: "action",
			Actions: []larkButton{{
				Tag:  "button",
				Text: larkText{Tag: "plain_text", Content: "Stop these alerts"},
				URL:  fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token),
				Type: "default",
			}},
		})
	}

	return larkPayload{
		MsgType: "interactive",
		Card: larkCard{
			Config: larkConfig{WideScreenMode: true},
			Header: larkHeader{
				Template: larkTemplate(alert.ToStatus),
				Title:    larkText{Tag: "plain_text", Content: title},
			},
			Elements: elements,
		},
	}
}

// larkCall posts one message and reads the code Lark put in its answer.
func (n *Notifier) larkCall(ctx context.Context, webhookURL, secret string, payload larkPayload) larkResult {
	if secret != "" {
		timestamp := time.Now().Unix()
		payload.Timestamp = strconv.FormatInt(timestamp, 10)
		payload.Sign = larkSign(secret, timestamp)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return larkResult{Error: fmt.Sprintf("write the message: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return larkResult{Error: fmt.Sprintf("build the call: %v", err)}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", n.userAgent())

	response, err := n.client.Do(request)
	if err != nil {
		return larkResult{Error: err.Error()}
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	answer := strings.TrimSpace(string(answered))
	var decoded struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(answered, &decoded); err != nil {
		return larkResult{Error: fmt.Sprintf("lark answered %s with an unreadable body: %s", response.Status, answer)}
	}

	result := larkResult{Code: decoded.Code, Message: decoded.Msg}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		result.Error = fmt.Sprintf("lark answered HTTP %s: %s", response.Status, larkAnswer(decoded.Code, decoded.Msg, answer))
		return result
	}
	if decoded.Code != 0 {
		result.Error = fmt.Sprintf("lark answered code %d: %s", decoded.Code, decoded.Msg)
		return result
	}
	result.OK = true
	return result
}

// larkSign computes the signature a custom bot expects for this request.
func larkSign(secret string, timestamp int64) string {
	key := fmt.Sprintf("%d\n%s", timestamp, secret)
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func larkAnswer(code int, message, fallback string) string {
	if message != "" {
		return fmt.Sprintf("code %d: %s", code, message)
	}
	return fallback
}

func larkTemplate(status string) string {
	if template, ok := larkTemplates[status]; ok {
		return template
	}
	return "grey"
}
