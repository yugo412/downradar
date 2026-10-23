package notify

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"text/template"
	"time"

	"downradar.info/backend/internal/store"
)

// The wording of a Telegram message is a file rather than a string, the same way
// an email's is. A message is plain text: a chat is not a page, and the only
// markup worth having there is a line break.
//
//go:embed templates/*.txt
var messageFiles embed.FS

// Parsed once, when the process starts. A template that does not parse is a
// mistake in the code rather than anything a request can cause.
var messages = template.Must(template.New("messages").ParseFS(messageFiles, "templates/*.txt"))

// defaultTelegramAPI is the Bot API. It is a field rather than a constant so a
// deployment can point at a Bot API server of its own, and so a test can point
// at one it runs.
const defaultTelegramAPI = "https://api.telegram.org"

// TelegramResult is what one message came back with. Telegram answers a refusal
// with a 200 and a body that says ok is false, so what it says is the answer
// rather than the status alone.
type TelegramResult struct {
	OK    bool
	Error string
}

// telegramData is what the message template is written with. The statuses are
// the words the site uses, so a chat and the page it links to agree.
type telegramData struct {
	Test        bool
	VendorName  string
	What        string
	From        string
	To          string
	ChangedAt   string
	URL         string
	Unsubscribe string
}

// sendTelegram hands one delivery to the chat its channel names, through the bot
// that channel was set up with.
func (n *Notifier) sendTelegram(ctx context.Context, alert store.PendingAlert) error {
	if strings.TrimSpace(alert.ChatID) == "" {
		return errors.New("the telegram way has no chat")
	}

	text, err := n.telegramText(alert, false)
	if err != nil {
		return err
	}

	result := n.telegramCall(ctx, alert.TelegramBot, alert.ChatID, text)
	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

// TestTelegram sends the message a delivery would, with a made up change in its
// place, so somebody can see their chat answer before they rely on it. It is the
// same ok-to-major move the webhook is tried with.
func (n *Notifier) TestTelegram(ctx context.Context, serviceID int64, botToken, chatID string) (TelegramResult, error) {
	if strings.TrimSpace(chatID) == "" {
		return TelegramResult{Error: "the chat is empty"}, errors.New("the chat is empty")
	}
	if strings.TrimSpace(botToken) == "" {
		return TelegramResult{Error: "the chat has no bot"}, errors.New("the chat has no bot")
	}

	snapshot, err := n.store.ServiceSnapshot(ctx, serviceID)
	if err != nil {
		return TelegramResult{Error: err.Error()}, err
	}

	alert := store.PendingAlert{
		Kind:       store.ChannelTelegram,
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

	text, err := n.telegramText(alert, true)
	if err != nil {
		return TelegramResult{Error: err.Error()}, err
	}
	return n.telegramCall(ctx, botToken, chatID, text), nil
}

// telegramText is the message one delivery carries. A test says that it is one
// and carries no way out, because nothing has been saved to stop yet.
func (n *Notifier) telegramText(alert store.PendingAlert, test bool) (string, error) {
	what := alert.VendorName
	if alert.ComponentName != "" {
		what = fmt.Sprintf("%s on %s", alert.ComponentName, alert.VendorName)
	}

	data := telegramData{
		Test:       test,
		VendorName: alert.VendorName,
		What:       what,
		From:       badgeFor(alert.FromStatus).Label,
		To:         badgeFor(alert.ToStatus).Label,
		ChangedAt:  alert.ChangedAt.UTC().Format("2006-01-02 15:04 MST"),
		URL:        fmt.Sprintf("%s/%s", n.publicURL, alert.VendorSlug),
	}
	if !test && alert.Token != "" {
		data.Unsubscribe = fmt.Sprintf("%s/alerts/unsubscribe?token=%s", n.publicURL, alert.Token)
	}

	var out bytes.Buffer
	if err := messages.ExecuteTemplate(&out, "telegram", data); err != nil {
		return "", fmt.Errorf("write the message: %w", err)
	}
	// A message is trimmed rather than left with the line breaks the template is
	// written with: what a chat shows is the message, not the file it came from.
	return strings.TrimSpace(out.String()), nil
}

// telegramCall hands one message over and reads what came back.
func (n *Notifier) telegramCall(ctx context.Context, botToken, chatID, text string) TelegramResult {
	if strings.TrimSpace(botToken) == "" {
		return TelegramResult{Error: "the chat has no bot"}
	}

	body, err := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	if err != nil {
		return TelegramResult{Error: fmt.Sprintf("write the message: %v", err)}
	}

	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := n.telegramRequest(ctx, botToken, "sendMessage", body, &answer); err != nil {
		return TelegramResult{Error: err.Error()}
	}
	if !answer.OK {
		// Telegram says what is wrong in words worth showing: a chat it cannot
		// find, or a bot that was blocked.
		if answer.Description == "" {
			return TelegramResult{Error: "telegram refused the message"}
		}
		return TelegramResult{Error: answer.Description}
	}
	return TelegramResult{OK: true}
}

// telegramRequest makes one call to the Bot API. The subscriber's own bot token
// is in the path because that is how the API is addressed, and the answer is read
// as JSON by the caller, since every answer has the same shape.
func (n *Notifier) telegramRequest(ctx context.Context, botToken, method string, body []byte, out any) error {
	base := n.telegramAPI
	if base == "" {
		base = defaultTelegramAPI
	}

	ctx, cancel := context.WithTimeout(ctx, webhookTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	url := fmt.Sprintf("%s/bot%s/%s", strings.TrimSuffix(base, "/"), botToken, method)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reader)
	if err != nil {
		return fmt.Errorf("build the call: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("User-Agent", n.userAgent())

	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("call telegram: %w", err)
	}
	defer response.Body.Close()

	answered, _ := io.ReadAll(io.LimitReader(response.Body, responseLimit))
	if err := json.Unmarshal(answered, out); err != nil {
		return fmt.Errorf("telegram answered %s", response.Status)
	}
	return nil
}
