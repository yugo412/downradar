package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// webhookRequest is the second way an alert can arrive: the URL to call, the
// method and the headers. An empty URL stops the calls; leaving it out leaves the
// way alone, because a body is written by the sender from what it finds.
type webhookRequest struct {
	URL     string             `json:"url"`
	Method  string             `json:"method"`
	Headers []store.AlertField `json:"headers"`

	// Auth says how a call is proved ours: standard signs it with a secret both
	// ends know, custom sends only the headers above. Left out keeps the existing
	// choice, so a page that predates the choice saves without changing it.
	Auth string `json:"auth"`

	// GenerateSecret asks for a fresh signing secret in the same save, which is
	// the only way one is made. The answer it comes back in is the only time it
	// can be read.
	GenerateSecret bool `json:"generateSecret"`
}

// telegramRequest is one chat and the bot that speaks to it. The bot belongs to
// the subscriber, so a save brings the token; a save without one changes the chat,
// and an empty chat stops the messages.
type telegramRequest struct {
	ChatID   string `json:"chatId"`
	BotToken string `json:"botToken"`
}

// slackRequest is the incoming webhook URL of a Slack channel, which names both
// the workspace and the channel. An empty URL stops the messages.
type slackRequest struct {
	URL string `json:"url"`
}

// discordRequest is the incoming webhook URL of a Discord channel; an empty one
// stops the messages.
type discordRequest struct {
	URL string `json:"url"`
}

// larkRequest is a Lark custom bot webhook and the secret it signs with when the
// group requires a signature. An empty URL stops the messages.
type larkRequest struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

type alertRequest struct {
	// Email is the address the alert should go to. An empty one stops the emails;
	// a request that says nothing about it, which is what the bell sends, leaves
	// the email way as it is.
	Email *string `json:"email"`

	// Webhook is the other way an alert can arrive. A page that shows both ways
	// sends it every time; the bell sends nothing about it.
	Webhook *webhookRequest `json:"webhook"`

	// Telegram is the chat a message should go to, and the bot that speaks to it.
	// It belongs to an account alone; an empty chat stops the messages, a save
	// without a bot keeps the one there, and leaving it out changes nothing.
	Telegram *telegramRequest `json:"telegram"`

	// Slack is the fourth way: the incoming webhook URL of a Slack channel. A
	// page that shows the way sends it every time, and an empty URL stops the
	// messages; a request that says nothing about it leaves the way as it is.
	Slack *slackRequest `json:"slack"`

	// Discord is the fifth way: a Discord channel's incoming webhook URL.
	Discord *discordRequest `json:"discord"`

	// Lark is the sixth way: a custom bot webhook, and the secret that signs a
	// request when the group requires it.
	Lark *larkRequest `json:"lark"`

	// What the email reports on: WholeService is the vendor's own status, Components
	// names the ones the vendor page shows. Naming neither is the old shape and means
	// the whole vendor; a webhook ignores both, so it needs no pick.
	WholeService *bool    `json:"wholeService"`
	Components   []string `json:"components"`
}

// subscribeAlert takes what somebody wants to hear about one vendor. An address
// needs no account, while a webhook hangs off an account; a signed in visitor's id
// is recorded too. Asking again replaces what is watched with what was chosen.
func (h *handlers) subscribeAlert(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	// The body holds a component list and a handful of headers.
	var body alertRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	// The address is optional: an alert can arrive by webhook alone, and a request
	// that clears it is somebody saying they would rather not be emailed. A
	// request that says nothing about it is the bell, which only ever had one.
	address := ""
	saveEmail := body.Email != nil
	if body.Email != nil && strings.TrimSpace(*body.Email) != "" {
		parsed, problem := parseEmail(*body.Email)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		address = parsed
	}

	var hook *store.AlertWebhook
	newSecret := false
	if body.Webhook != nil {
		parsed, problem := parseWebhook(*body.Webhook)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		hook, newSecret = parsed, body.Webhook.GenerateSecret
	}

	// A chat is named rather than typed freely: the message goes wherever this says,
	// and Telegram decides whether it can be reached. An empty chat stops the messages.
	// The bot is the subscriber's own; one not named on a save is the one already saved.
	var telegram *store.AlertTelegram
	saveTelegram := body.Telegram != nil
	if body.Telegram != nil {
		chatID := ""
		if strings.TrimSpace(body.Telegram.ChatID) != "" {
			parsed, problem := parseChatID(body.Telegram.ChatID)
			if problem != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
				return
			}
			chatID = parsed
		}

		botToken := ""
		if strings.TrimSpace(body.Telegram.BotToken) != "" {
			parsed, problem := parseBotToken(body.Telegram.BotToken)
			if problem != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
				return
			}
			botToken = parsed
		}

		telegram = &store.AlertTelegram{ChatID: chatID, BotToken: botToken}
	}

	// A Slack channel is named by the incoming webhook URL Slack gives for it. An
	// empty URL stops the messages, the same bargain an address has.
	var slack *store.AlertSlack
	saveSlack := body.Slack != nil
	if body.Slack != nil {
		webhookURL, problem := parseSlack(body.Slack.URL)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		slack = &store.AlertSlack{URL: webhookURL}
	}

	// A Discord channel is named by the incoming webhook URL Discord gives for it;
	// an empty URL stops the messages.
	var discord *store.AlertDiscord
	saveDiscord := body.Discord != nil
	if body.Discord != nil {
		webhookURL, problem := parseDiscord(body.Discord.URL)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		discord = &store.AlertDiscord{URL: webhookURL}
	}

	// A Lark group is named by its custom bot webhook. The secret is optional,
	// but when it is present every call is signed with it.
	var lark *store.AlertLark
	saveLark := body.Lark != nil
	if body.Lark != nil {
		webhookURL, secret, problem := parseLark(body.Lark.URL, body.Lark.Secret)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		lark = &store.AlertLark{URL: webhookURL, Secret: secret}
	}

	// A request that names no way at all would write a subscription with nothing
	// to send, which is not an alert. Clearing a way is naming it: an empty
	// address is how somebody stops the emails.
	if body.Email == nil && body.Webhook == nil && body.Telegram == nil && body.Slack == nil &&
		body.Discord == nil && body.Lark == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "An email address, a webhook, a chat, a Slack channel, a Discord channel or a Lark group is required",
		})
		return
	}

	// The chat is set only where the bot is: a save naming one while Telegram is not
	// configured would be taken and never sent. Stopping a way is always allowed,
	// because what is already there should be able to be taken away.
	if telegram != nil && telegram.ChatID != "" && !h.telegramEnabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Telegram is not available right now",
		})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// A request that names neither is the older shape and meant the vendor's own
	// status; the bell only sent that, and an old page sends it still. An empty list
	// names nothing, refused below rather than read as the whole vendor.
	whole := body.WholeService == nil && body.Components == nil
	if body.WholeService != nil {
		whole = *body.WholeService
	}

	componentIDs, err := h.store.ComponentIDs(r.Context(), serviceID, body.Components)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "One of those components is not part of this service any more. Reload the page and pick again.",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// A way that reports on a pick needs one; a webhook hears about the vendor
	// itself.
	watchesByPick := (saveEmail && address != "") || (telegram != nil && telegram.ChatID != "") ||
		(slack != nil && slack.URL != "") || (discord != nil && discord.URL != "") ||
		(lark != nil && lark.URL != "")
	if watchesByPick && !whole && len(componentIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Pick at least one thing to be told about",
		})
		return
	}

	var userID *int64
	var role string
	user, err := h.authenticate(r)
	switch {
	case err == nil:
		userID = &user.ID
		role = user.Role
	case !errors.Is(err, store.ErrNotFound):
		h.fail(w, r, err)
		return
	}

	calls := hook != nil && hook.URL != ""

	// An empty address from somebody with no account says nothing: there is no
	// held alert to stop, and the address is the only thing they have. A
	// subscriber clears an address to stop the emails that go to it.
	if userID == nil && saveEmail && address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "An email address is required"})
		return
	}

	// Someone who is not signed in gets one whole-vendor alert: components and the
	// account-held ways need a subscriber. Staff are not held to a count.
	switch {
	case userID == nil:
		if len(componentIDs) > 0 {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Components are for subscribers. Sign in to follow one.",
			})
			return
		}
		if calls {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Webhooks are for subscribers. Sign in to add one.",
			})
			return
		}
		if saveTelegram {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Telegram is for subscribers. Sign in to add one.",
			})
			return
		}
		if saveSlack {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Slack is for subscribers. Sign in to add one.",
			})
			return
		}
		if saveDiscord {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Discord is for subscribers. Sign in to add one.",
			})
			return
		}
		if saveLark {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "Lark is for subscribers. Sign in to add one.",
			})
			return
		}
		elsewhere, err := h.store.AlertElsewhere(r.Context(), address, serviceID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.fail(w, r, err)
			return
		}
		if elsewhere != nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf(
					"That address already has an alert for %s. Unsubscribe from that one first, or sign in to add more.",
					elsewhere.Name),
			})
			return
		}
	case role == auth.RoleSubscriber:
		// The cap counts vendors rather than the things watched inside one, so this
		// asks for a single place in it unless the alert is already there: replacing
		// one or stopping a way takes no new place.
		watched, err := h.store.WatchedServiceCount(r.Context(), *userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		already := false
		if address != "" {
			already, err = h.store.AddressWatchesService(r.Context(), address, serviceID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
		}
		if !already {
			already, err = h.store.UserWatchesService(r.Context(), *userID, serviceID)
			if err != nil {
				h.fail(w, r, err)
				return
			}
		}
		adding := 1
		if already {
			adding = 0
		}
		limit, err := h.userAlertLimit(r, *userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if watched+adding > limit {
			services := "services"
			if limit == 1 {
				services = "service"
			}
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf(
					"Your account has reached its limit of %d %s. Remove one from your alerts page first.",
					limit, services),
			})
			return
		}
	}

	// Each channel a save creates takes a token of its own, which is what stops
	// that way and what a webhook is checked against. One that is already there
	// keeps its token.
	change := store.AlertChange{Targets: store.AlertTargets{Whole: whole, Components: componentIDs}}
	if saveEmail {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Email, change.EmailToken = &address, token
	}
	if hook != nil {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Webhook, change.WebhookToken = hook, token
	}
	if saveTelegram {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Telegram, change.TelegramToken = telegram, token
	}
	if slack != nil {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Slack, change.SlackToken = slack, token
	}
	if discord != nil {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Discord, change.DiscordToken = discord, token
	}
	if lark != nil {
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		change.Lark, change.LarkToken = lark, token
	}

	secret := ""
	var saveErr error
	if newSecret {
		secret, saveErr = h.store.SubscribeAlertWithSecret(r.Context(), serviceID, userID, change)
	} else {
		saveErr = h.store.SubscribeAlert(r.Context(), serviceID, userID, change)
	}
	if saveErr != nil {
		switch {
		case errors.Is(saveErr, store.ErrNoBotToken):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A bot token is required for a chat",
			})
			return
		case errors.Is(saveErr, store.ErrWebhookSecretMissing):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Generate a signing secret for this webhook first",
			})
			return
		case errors.Is(saveErr, store.ErrWebhookSecretUnused):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A signing secret is only for signed calls",
			})
			return
		}
		h.fail(w, r, saveErr)
		return
	}

	// The answer says nothing about whether the address was already subscribed,
	// so this cannot be used to ask whether somebody else's is. The secret is the
	// exception: it is handed back once, and never again.
	answer := map[string]any{"status": "subscribed"}
	if secret != "" {
		answer["webhookSecret"] = secret
	}
	h.log.Info("alert subscription saved", "vendor", name, "email", address != "" && saveEmail,
		"webhook", calls, "telegram", telegram != nil && telegram.ChatID != "",
		"slack", slack != nil && slack.URL != "",
		"discord", discord != nil && discord.URL != "",
		"lark", lark != nil && lark.URL != "",
		"signed_in", userID != nil, "webhook_secret", secret != "",
		"whole", whole, "components", len(componentIDs))
	writeJSON(w, http.StatusCreated, answer)
}

// webhookMethods are the methods a webhook can be sent with. They are the ones
// that carry a body, which is what a call is: a GET has nothing to say and would
// arrive with the news missing.
var webhookMethods = map[string]bool{
	http.MethodPost:  true,
	http.MethodPut:   true,
	http.MethodPatch: true,
}

// parseWebhook reads what a request says about a webhook and refuses anything the
// sender could not use. Nothing here reads inside a header value: it may carry
// placeholders, and what one turns into is the sender's business.
func parseWebhook(body webhookRequest) (*store.AlertWebhook, string) {
	rawURL := strings.TrimSpace(body.URL)
	if rawURL == "" {
		// An empty URL is how a request stops the calls.
		return &store.AlertWebhook{}, ""
	}
	if len(rawURL) > 1024 {
		return nil, "That webhook URL is too long"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "That does not look like a webhook URL"
	}

	method := strings.ToUpper(strings.TrimSpace(body.Method))
	if method == "" {
		method = http.MethodPost
	}
	if !webhookMethods[method] {
		return nil, "The method must be POST, PUT or PATCH"
	}

	auth := strings.ToLower(strings.TrimSpace(body.Auth))
	switch auth {
	case "", store.WebhookAuthStandard, store.WebhookAuthCustom:
	default:
		return nil, "The authentication must be standard or custom"
	}
	if body.GenerateSecret && auth != store.WebhookAuthStandard {
		return nil, "A signing secret is only for signed calls"
	}

	headers, problem := parseFields(body.Headers, "header")
	if problem != "" {
		return nil, problem
	}
	for _, header := range headers {
		if strings.ContainsAny(header.Name+header.Value, "\r\n") {
			return nil, "A header cannot hold a line break"
		}
	}

	return &store.AlertWebhook{URL: rawURL, Method: method, Headers: headers, Auth: auth}, ""
}

// parseSlack reads the incoming webhook URL of a Slack channel and refuses
// anything the sender could not call. Here it only has to be an address, the same
// bargain a webhook gets; the page checks that it looks like Slack's own.
func parseSlack(raw string) (string, string) {
	rawURL := strings.TrimSpace(raw)
	if rawURL == "" {
		return "", ""
	}
	if len(rawURL) > 1024 {
		return "", "That Slack webhook URL is too long"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "That does not look like a Slack webhook URL"
	}
	return rawURL, ""
}

// parseDiscord reads a Discord webhook URL. The page checks its shape; here it
// only has to be an http(s) address.
func parseDiscord(raw string) (string, string) {
	rawURL := strings.TrimSpace(raw)
	if rawURL == "" {
		return "", ""
	}
	if len(rawURL) > 1024 {
		return "", "That Discord webhook URL is too long"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "That does not look like a Discord webhook URL"
	}
	return rawURL, ""
}

// parseLark reads a custom bot webhook and the optional signing secret. The page
// checks the Lark host and path; here it only has to be an http(s) address.
func parseLark(rawURL, rawSecret string) (string, string, string) {
	webhookURL := strings.TrimSpace(rawURL)
	secret := strings.TrimSpace(rawSecret)
	if webhookURL == "" {
		return "", secret, ""
	}
	if len(webhookURL) > 1024 {
		return "", "", "That Lark webhook URL is too long"
	}
	if len(secret) > 512 {
		return "", "", "That Lark signing secret is too long"
	}

	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", "That does not look like a Lark webhook URL"
	}
	return webhookURL, secret, ""
}

// parseFields reads a list of names and values, and drops the empty rows a screen
// can leave behind. A row with a value but no name has nowhere to go, so it is
// refused rather than sent as an empty name.
func parseFields(in []store.AlertField, what string) ([]store.AlertField, string) {
	out := make([]store.AlertField, 0, len(in))
	for _, field := range in {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			if strings.TrimSpace(field.Value) == "" {
				continue
			}
			return nil, fmt.Sprintf("A %s needs a name", what)
		}
		out = append(out, store.AlertField{Name: name, Value: field.Value})
	}
	return out, ""
}

// testWebhook makes one call with what the page is holding, saved or not, and
// answers with what came back so a subscriber can watch their own end work. A
// refusing far end is an answer, not a failure here, and the call is recorded like a delivery.
func (h *handlers) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body webhookRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	hook, problem := parseWebhook(body)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if hook.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A webhook URL is required"})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	user := h.user(r)

	// A signed try uses the saved secret, which the page is never handed, so a
	// webhook without one can only be tried after it has one; pretending to sign
	// would teach the subscriber the wrong thing about their own end.
	if hook.Auth == store.WebhookAuthStandard {
		saved, err := h.store.UserAlert(r.Context(), user.ID, serviceID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			h.fail(w, r, err)
			return
		}
		if saved == nil || saved.Webhook == nil || !saved.Webhook.Signed {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "Generate a signing secret before trying a signed call",
			})
			return
		}
		hook.Signing = saved.Webhook.Signing
	}

	result, _ := h.notifier.TestWebhook(r.Context(), serviceID, *hook)
	if err := h.store.LogWebhookCall(r.Context(), &user.ID, serviceID, nil, store.WebhookCall{
		Kind:     store.CallTest,
		URL:      hook.URL,
		Method:   hook.Method,
		Status:   statusOrNil(result.Status),
		Response: result.Response,
		Error:    result.Error,
	}); err != nil {
		h.log.Error("failed to record a webhook try", "error", err)
	}

	h.log.Info("webhook tried from the alert page", "vendor", name, "user", user.Email,
		"status", result.Status, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   statusOrNil(result.Status),
		"response": result.Response,
		"error":    result.Error,
	})
}

// testTelegram sends the message a delivery would, through the bot the page holds
// or the one the chat is saved with, so a chat can be checked first. Telegram's
// answer is handed back as it is.
func (h *handlers) testTelegram(w http.ResponseWriter, r *http.Request) {
	if !h.telegramEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Telegram is not available right now",
		})
		return
	}

	var body telegramRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	chatID, problem := parseChatID(body.ChatID)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	botToken := ""
	if strings.TrimSpace(body.BotToken) != "" {
		parsed, problem := parseBotToken(body.BotToken)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		botToken = parsed
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// The token is a secret a page is never handed back, so a chat that is already
	// saved is tried with the bot it was saved with.
	if botToken == "" {
		botToken, err = h.store.TelegramBot(r.Context(), h.user(r).ID, serviceID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if botToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A bot token is required for a chat",
			})
			return
		}
	}

	result, err := h.notifier.TestTelegram(r.Context(), serviceID, botToken, chatID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("telegram tried from the alert page", "vendor", name,
		"user", h.user(r).Email, "ok", result.OK, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{"ok": result.OK, "error": result.Error})
}

// testSlack posts the message a delivery would, through the webhook the page holds
// or the one the channel is saved with, so a channel can be checked first. Slack's
// answer is handed back as it is.
func (h *handlers) testSlack(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body slackRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	webhookURL, problem := parseSlack(body.URL)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// The URL is a secret the page shows masked, so a way that is already saved is
	// tried with the URL it was saved with when the field is empty.
	if webhookURL == "" {
		webhookURL, err = h.store.SlackWebhook(r.Context(), h.user(r).ID, serviceID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if webhookURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A Slack webhook URL is required",
			})
			return
		}
	}

	result, err := h.notifier.TestSlack(r.Context(), serviceID, webhookURL)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("slack tried from the alert page", "vendor", name,
		"user", h.user(r).Email, "ok", result.OK, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{"ok": result.OK, "error": result.Error})
}

// testDiscord posts through the held URL or the saved one, so a channel can be
// checked before it is relied on.
func (h *handlers) testDiscord(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body discordRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	webhookURL, problem := parseDiscord(body.URL)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// A saved URL is tried with the one it was saved with when the field is empty.
	if webhookURL == "" {
		webhookURL, err = h.store.DiscordWebhook(r.Context(), h.user(r).ID, serviceID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if webhookURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A Discord webhook URL is required",
			})
			return
		}
	}

	result, err := h.notifier.TestDiscord(r.Context(), serviceID, webhookURL)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("discord tried from the alert page", "vendor", name,
		"user", h.user(r).Email, "ok", result.OK, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{"ok": result.OK, "error": result.Error})
}

// testLark posts through the held webhook or the saved one, so a group can be
// checked before it is relied on. Lark's code is handed back with its answer.
func (h *handlers) testLark(w http.ResponseWriter, r *http.Request) {
	if !h.alertsEnabled || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Alerts are not available right now",
		})
		return
	}

	var body larkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	webhookURL, secret, problem := parseLark(body.URL, body.Secret)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// A saved URL and its secret are tried when the fields are empty, so the page
	// does not have to hand back what it shows masked.
	if webhookURL == "" {
		webhookURL, secret, err = h.store.LarkWebhook(r.Context(), h.user(r).ID, serviceID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if webhookURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "A Lark webhook URL is required",
			})
			return
		}
	}

	result, err := h.notifier.TestLark(r.Context(), serviceID, webhookURL, secret)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("lark tried from the alert page", "vendor", name,
		"user", h.user(r).Email, "ok", result.OK, "code", result.Code, "error", result.Error)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    result.OK,
		"code":  result.Code,
		"error": result.Error,
	})
}

// statusOrNil writes "no answer" as a null rather than a zero, so a call that was
// never answered does not read as one that got a 0.
func statusOrNil(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}

// alertPauseRequest is one way of one alert being stopped or started. It names a
// way rather than a row, because what it changes is that way and nothing else.
type alertPauseRequest struct {
	Kind   string `json:"kind"`
	Paused bool   `json:"paused"`
}

// setAlertChannelPaused stops one way of an alert arriving, or starts it again,
// without taking away the address or the URL it was told: stopping a way for good
// is clearing that field and saving, which is what the page's own field does.
func (h *handlers) setAlertChannelPaused(w http.ResponseWriter, r *http.Request) {
	var body alertPauseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.Kind != store.ChannelEmail && body.Kind != store.ChannelWebhook &&
		body.Kind != store.ChannelTelegram && body.Kind != store.ChannelSlack &&
		body.Kind != store.ChannelDiscord && body.Kind != store.ChannelLark {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Kind must be email, webhook, telegram, slack, discord or lark",
		})
		return
	}

	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	err = h.store.SetChannelPaused(r.Context(), h.user(r).ID, serviceID, body.Kind, body.Paused)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This alert has no such way"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert way paused", "vendor", name, "user", h.user(r).Email,
		"way", body.Kind, "paused", body.Paused)
	w.WriteHeader(http.StatusNoContent)
}

// getAlert is what the unsubscribe page reads before it offers the button: which
// vendor the link is about, and whether it was already used.
func (h *handlers) getAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	view, err := h.store.AlertByToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This link is not valid"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vendorSlug":     view.VendorSlug,
		"vendorName":     view.VendorName,
		"kind":           view.Kind,
		"destination":    maskDestination(view.Kind, view.Email),
		"createdAt":      view.CreatedAt,
		"paused":         view.PausedAt != nil,
		"unsubscribedAt": view.UnsubscribedAt,
	})
}

// pauseAlert is the other thing the link in a message can do: stop that way for a
// while without taking it away. The token proves ownership, and pressing twice is safe.
func (h *handlers) pauseAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	err := h.store.SetChannelPausedByToken(r.Context(), r.PathValue("token"), body.Paused)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This link is not valid"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert way paused from its link", "paused", body.Paused)
	w.WriteHeader(http.StatusNoContent)
}

// listUserAlerts is the account screen: everything this person asked to be told
// about, including paused and ended ones so they can start again. The address is
// shown in full because it is their own screen and knowing which inbox is the point.
func (h *handlers) listUserAlerts(w http.ResponseWriter, r *http.Request) {
	user := h.user(r)

	alerts, err := h.store.UserAlerts(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	views := make([]map[string]any, 0, len(alerts))
	for _, alert := range alerts {
		views = append(views, map[string]any{
			"id":           alert.ID,
			"vendorSlug":   alert.VendorSlug,
			"vendorName":   alert.VendorName,
			"vendorStatus": alert.VendorStatus,
			"email":        alert.Email,
			"state":        alert.State(),
			"createdAt":    alert.CreatedAt,
			"wholeService": alert.WholeService,
			"components":   alert.Components,
			"channels":     alert.Channels,
		})
	}

	// The screen says how full the account is, so it can warn before the next
	// vendor is refused. Only a subscriber is held to a count, so staff are told
	// nothing and the warning stays away.
	var limit *int
	used := 0
	if user.Role == auth.RoleSubscriber {
		value, err := h.userAlertLimit(r, user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		limit = &value
		used, err = h.store.WatchedServiceCount(r.Context(), user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": views, "limit": limit, "used": used})
}

// userAlertLimit is how many vendors this account may watch at once. It is read
// from the account's own row, so one subscriber can differ from another; the
// environment is only the value a new account starts with.
func (h *handlers) userAlertLimit(r *http.Request, userID int64) (int, error) {
	limit, ok, err := h.store.UserLimit(r.Context(), userID, store.LimitAlert)
	if err != nil {
		return 0, err
	}
	if !ok {
		return h.alertLimit, nil
	}
	return limit, nil
}

type alertStateRequest struct {
	State string `json:"state"`
}

// setUserAlertState is pause, resume and unsubscribe, which are the same change
// of state asked for three ways.
func (h *handlers) setUserAlertState(w http.ResponseWriter, r *http.Request) {
	var body alertStateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if !store.AlertStateValid(body.State) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "State must be active, paused or unsubscribed",
		})
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}

	err = h.store.SetUserAlertState(r.Context(), h.user(r).ID, id, body.State)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert state changed", "user", h.user(r).Email, "alert", id, "state", body.State)
	w.WriteHeader(http.StatusNoContent)
}

// deleteUserAlertRow is the account screen's Delete: the row goes, rather than
// staying behind as an ended one.
func (h *handlers) deleteUserAlertRow(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}

	err = h.store.DeleteUserAlert(r.Context(), h.user(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Alert not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert deleted", "user", h.user(r).Email, "alert", id)
	w.WriteHeader(http.StatusNoContent)
}

// getUserAlert is what the alert page reads before it draws: the ways this person
// has here, whether it is paused, what it watches and the last calls. The address is
// shown in full because it is their own screen; a signed out alert belongs to the address.
func (h *handlers) getUserAlert(w http.ResponseWriter, r *http.Request) {
	serviceID, _, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// The calls are read whether or not there is an alert: a webhook can be tried
	// from the page before anything is saved, and what it answered is worth
	// showing the next time the page opens. The page shows them twenty at a time,
	// so it reads everything the store keeps.
	calls, err := h.store.WebhookCalls(r.Context(), h.user(r).ID, serviceID, store.WebhookCallsKept)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	channelStatus, err := h.store.ChannelDeliveryStatuses(r.Context(), h.user(r).ID, serviceID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	view, err := h.store.UserAlert(r.Context(), h.user(r).ID, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{
			"subscribed":      false,
			"calls":           calls,
			"channelStatus":   channelStatus,
			"telegramEnabled": h.telegramEnabled,
			"telegramBot":     "",
			"slackEnabled":    h.slack != nil && h.alertsEnabled,
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"subscribed":      true,
		"email":           view.Email,
		"paused":          view.State() == store.AlertPaused,
		"emailPaused":     view.EmailPaused,
		"webhookPaused":   view.WebhookPaused,
		"telegramPaused":  view.TelegramPaused,
		"slackPaused":     view.SlackPaused,
		"discordPaused":   view.DiscordPaused,
		"larkPaused":      view.LarkPaused,
		"wholeService":    view.WholeService,
		"components":      view.ComponentIDs,
		"webhook":         view.Webhook,
		"telegram":        view.Telegram,
		"slack":           view.Slack,
		"slackChannel":    view.SlackChannel,
		"discord":         view.Discord,
		"lark":            view.Lark,
		"larkSecret":      view.LarkSecret,
		"telegramEnabled": h.telegramEnabled,
		"slackEnabled":    h.slack != nil && h.alertsEnabled,
		// The bot is the owner's own, and the page shows it masked with a switch to
		// reveal it, so it can be seen and replaced rather than only pasted again.
		"telegramBot":   view.TelegramBot,
		"calls":         calls,
		"channelStatus": channelStatus,
	})
}

// deleteUserAlert is the way out for somebody signed in. The unsubscribe link in
// an email needs a token; this needs only the account the alert was made with.
func (h *handlers) deleteUserAlert(w http.ResponseWriter, r *http.Request) {
	serviceID, name, err := h.store.PublicService(r.Context(), r.PathValue("slug"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Vendor not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	stopped, err := h.store.UnsubscribeUserAlert(r.Context(), h.user(r).ID, serviceID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alerts stopped from the vendor page", "vendor", name, "subscriptions", stopped)
	w.WriteHeader(http.StatusNoContent)
}

// maskDestination names where messages go without repeating it, because the page
// is reached through a link that can be forwarded. A chat or channel is named, not
// spelled out: there is nothing in it the person does not already know.
func maskDestination(kind, address string) string {
	if kind == store.ChannelTelegram {
		return "your chat"
	}
	if kind == store.ChannelSlack {
		return "your Slack channel"
	}
	if kind == store.ChannelDiscord {
		return "your Discord channel"
	}

	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" {
		return "your address"
	}
	return string([]rune(local)[:1]) + "***@" + domain
}

// unsubscribeAlert is the button, not the link. Mail clients and scanners fetch
// the links they are sent, so the footer link only opens the page above; this is
// what stops the alerts, and it is safe to press twice.
func (h *handlers) unsubscribeAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	err := h.store.UnsubscribeAlert(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This link is not valid"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("alert unsubscribed", "token", shortToken(r.PathValue("token")))
	w.WriteHeader(http.StatusNoContent)
}

// parseEmail normalises an address and refuses anything that is not one. The same
// normalisation is used for accounts, so case cannot subscribe twice; the alert
// and contact forms both take an address from a visitor.
func parseEmail(raw string) (string, string) {
	email := auth.NormalizeEmail(raw)
	if email == "" {
		return "", "An email address is required"
	}
	if len(email) > 254 {
		return "", "That email address is too long"
	}

	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || !strings.Contains(email, ".") {
		return "", "That does not look like an email address"
	}
	return email, ""
}

// parseChatID reads the chat a Telegram message should go to: a number or the @name
// of a public channel. Which chats exist is Telegram's to say, so the test message
// is the real check; here it only has to look like one of the two.
func parseChatID(raw string) (string, string) {
	chat := strings.TrimSpace(raw)
	if chat == "" {
		return "", "A chat is required"
	}
	if len(chat) > 64 {
		return "", "That chat is too long"
	}

	if name, ok := strings.CutPrefix(chat, "@"); ok {
		if !isChatName(name) {
			return "", "A channel name is letters, numbers and underscores, like @my_channel"
		}
		return chat, ""
	}

	// A group or a channel has a negative number, so the sign is part of it.
	digits := strings.TrimPrefix(chat, "-")
	if digits == "" || !isDigits(digits) {
		return "", "A chat is a number, or the @name of a channel"
	}
	return chat, ""
}

// parseBotToken reads the token of the bot that speaks to a chat. Telegram is what
// can say whether it is good, so the test message is the real check; here it only
// has to look like a token rather than a stray paste.
func parseBotToken(raw string) (string, string) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", "A bot token is required"
	}
	if len(token) > 128 {
		return "", "That bot token is too long"
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", "A bot token has no spaces in it"
	}

	id, secret, ok := strings.Cut(token, ":")
	if !ok || !isDigits(id) || secret == "" {
		return "", "A bot token looks like 123456789:AAH..."
	}
	return token, ""
}

func isDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func isChatName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// shortToken is enough of a token to recognise one in a log line, and not enough
// to use.
func shortToken(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}
