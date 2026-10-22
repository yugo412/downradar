package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// How long each link is good for: a week to confirm an address, an hour to
// choose a new password, and minutes to sign in, while the person is at their mail.
const (
	verifyTokenTTL = 7 * 24 * time.Hour
	resetTokenTTL  = time.Hour
	loginTokenTTL  = 15 * time.Minute
)

// startSession gives somebody a signed in browser. Signing in, following a link
// and registering all need it, and they must not drift apart.
func (h *handlers) startSession(w http.ResponseWriter, r *http.Request, user *store.User, remember bool) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}

	expires := time.Now().Add(h.sessionTTL)
	if err := h.store.CreateSession(r.Context(), auth.HashToken(token), user.ID, expires); err != nil {
		return err
	}

	h.setSessionCookie(w, token, expires, remember)
	return nil
}

// localPath keeps the page to open after signing in on this site, so a link that
// carries one cannot be turned into a way off it.
func localPath(next string) string {
	next = strings.TrimSpace(next)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return ""
	}
	return next
}

// sendVerification makes a fresh link and emails it. A failure to send is logged
// rather than raised: the account exists either way and the banner offers to try
// again. The context is detached from the request so a gone browser does not stop the mail.
func (h *handlers) sendVerification(ctx context.Context, user *store.User) {
	if h.accountMailer == nil {
		return
	}

	token, err := auth.NewToken()
	if err != nil {
		h.log.Error("could not make a confirmation token", "user", user.Email, "error", err)
		return
	}
	if err := h.store.CreateUserToken(ctx, user.ID, store.UserTokenVerify,
		auth.HashToken(token), time.Now().Add(verifyTokenTTL)); err != nil {
		h.log.Error("could not record a confirmation token", "user", user.Email, "error", err)
		return
	}

	if err := h.accountMailer.Verify(ctx, user.Email, user.Name, token); err != nil {
		h.log.Error("could not send the confirmation email", "user", user.Email, "error", err)
		return
	}
	h.log.Info("confirmation email sent", "user", user.Email)
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// register makes an account from the site itself, always a subscriber. An address
// on the site's own domain gets a reply that says no more than that the account
// could not be made, so it cannot be used to map our mailboxes.
func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Accounts cannot be created right now",
		})
		return
	}

	var body registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	name := clean(sanitize(body.Name))
	email := auth.NormalizeEmail(body.Email)

	if problem := validateDisplayName(name); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That does not look like an email address",
		})
		return
	}
	if auth.OnReservedDomain(email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "That account could not be created",
		})
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	user, err := h.store.RegisterUser(r.Context(), email, name, hash)
	if errors.Is(err, store.ErrEmailTaken) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "That email address is already registered",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// One less step for the visitor, and the banner on the site keeps asking for
	// the address to be confirmed until it is. A new account stays signed in
	// after the browser closes, because there is nothing older to compare it to.
	if err := h.startSession(w, r, user, true); err != nil {
		h.fail(w, r, err)
		return
	}

	h.sendVerification(context.WithoutCancel(r.Context()), user)
	h.log.Info("account registered", "user", user.Email, "ip", h.clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

type tokenRequest struct {
	Token string `json:"token"`
}

// verifyEmail spends the link that proves an address belongs to this account.
func (h *handlers) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body tokenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A token is required"})
		return
	}

	hash := auth.HashToken(body.Token)
	user, err := h.store.UserByUserToken(r.Context(), store.UserTokenVerify, hash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	spent, err := h.store.SpendUserToken(r.Context(), store.UserTokenVerify, hash)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !spent {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}

	if err := h.store.SetUserVerified(r.Context(), user.ID); err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("address confirmed", "user", user.Email)
	writeJSON(w, http.StatusOK, map[string]any{"email": user.Email})
}

// resendVerification is for somebody signed in whose link never arrived, or who
// has changed their address since.
func (h *handlers) resendVerification(w http.ResponseWriter, r *http.Request) {
	user := h.user(r)
	if user.Verified() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "already confirmed"})
		return
	}
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "The confirmation email cannot be sent right now",
		})
		return
	}

	h.sendVerification(context.WithoutCancel(r.Context()), user)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "sent"})
}

type forgotRequest struct {
	Email string `json:"email"`
}

// forgotPassword sends a reset link when the address has an account, and answers
// the same way either way: whether an address is registered is not a stranger's
// to ask.
func (h *handlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Password resets are not available right now",
		})
		return
	}

	var body forgotRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	email := auth.NormalizeEmail(body.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That does not look like an email address",
		})
		return
	}

	user, err := h.store.UserByEmail(r.Context(), email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Nothing to do, and nothing to say about it.
	case err != nil:
		h.fail(w, r, err)
		return
	case !user.Disabled():
		ctx := context.WithoutCancel(r.Context())
		token, err := auth.NewToken()
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if err := h.store.CreateUserToken(ctx, user.ID, store.UserTokenReset,
			auth.HashToken(token), time.Now().Add(resetTokenTTL)); err != nil {
			h.fail(w, r, err)
			return
		}
		if err := h.accountMailer.Reset(ctx, user.Email, user.Name, token); err != nil {
			h.log.Error("could not send the reset email", "user", user.Email, "error", err)
		}
	}

	h.log.Info("password reset asked for", "email", email, "ip", h.clientIP(r))
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "if that address has an account, a link is on its way",
	})
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// resetPassword sets a new password from a link, and ends every session: if the
// old password was known to somebody else, they are out.
func (h *handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body resetRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A token is required"})
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash := auth.HashToken(body.Token)
	user, err := h.store.UserByUserToken(r.Context(), store.UserTokenReset, hash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	passwordHash, err := auth.HashPassword(body.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	spent, err := h.store.SpendUserToken(r.Context(), store.UserTokenReset, hash)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !spent {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}

	if err := h.store.SetUserPassword(r.Context(), user.ID, passwordHash); err != nil {
		h.fail(w, r, err)
		return
	}
	ended, err := h.store.DeleteUserSessions(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("password reset", "user", user.Email, "sessions_ended", ended)
	writeJSON(w, http.StatusOK, map[string]any{"email": user.Email, "sessionsEnded": ended})
}

type linkRequest struct {
	Email string `json:"email"`
	// Next is the page to open once the link has signed the person in, so
	// somebody sent to the form from a page they asked for ends up back on it.
	Next string `json:"next"`
}

// sendLoginLink emails a link that signs the person in. An address without an
// account is refused rather than quietly turned into one: this is a way in, not
// a way to get an account.
func (h *handlers) sendLoginLink(w http.ResponseWriter, r *http.Request) {
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Sign in links are not available right now",
		})
		return
	}

	var body linkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	email := auth.NormalizeEmail(body.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That does not look like an email address",
		})
		return
	}

	user, err := h.store.UserByEmail(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		h.log.Info("sign in link asked for an address with no account",
			"email", email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "There is no account with that email address",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if user.Disabled() {
		h.log.Warn("sign in link refused, account disabled", "email", email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "That account cannot sign in",
		})
		return
	}

	// The answer waits for the mail server here rather than answering first:
	// unlike a reset, this message is the whole point of the request, and a
	// visitor whose mail could not be sent needs to hear it now.
	ctx := context.WithoutCancel(r.Context())
	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.store.CreateUserToken(ctx, user.ID, store.UserTokenLogin,
		auth.HashToken(token), time.Now().Add(loginTokenTTL)); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.accountMailer.Login(ctx, user.Email, user.Name, token, localPath(body.Next)); err != nil {
		h.log.Error("could not send the sign in link", "user", user.Email, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "The sign in link could not be sent, try again in a moment",
		})
		return
	}

	h.log.Info("sign in link sent", "user", user.Email, "ip", h.clientIP(r))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
}

type linkSessionRequest struct {
	Token    string `json:"token"`
	Remember bool   `json:"remember"`
}

// loginWithLink signs somebody in from the link in their inbox. The link proves
// the address, so an account that was never confirmed by hand becomes confirmed
// on the way in.
func (h *handlers) loginWithLink(w http.ResponseWriter, r *http.Request) {
	var body linkSessionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A token is required"})
		return
	}

	hash := auth.HashToken(body.Token)
	user, err := h.store.UserByUserToken(r.Context(), store.UserTokenLogin, hash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	spent, err := h.store.SpendUserToken(r.Context(), store.UserTokenLogin, hash)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !spent {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "This link is not valid any more",
		})
		return
	}

	if !user.Verified() {
		if err := h.store.SetUserVerified(r.Context(), user.ID); err != nil {
			h.fail(w, r, err)
			return
		}
		// The row the answer carries has to say so, or the banner on the site
		// keeps asking for an address that has just been proved.
		confirmed, err := h.store.UserByID(r.Context(), user.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		user = confirmed
	}

	if err := h.startSession(w, r, user, body.Remember); err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("signed in with a link", "user", user.Email, "role", user.Role)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}
