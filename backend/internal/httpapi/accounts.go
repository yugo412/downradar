package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// How long each link is good for. Confirming an address is not urgent, and a
// link in a week old inbox should still work; choosing a new password is.
const (
	verifyTokenTTL = 7 * 24 * time.Hour
	resetTokenTTL  = time.Hour
)

// startSession gives somebody a signed in browser. Signing in and registering
// both need it, and the two must not drift apart.
func (h *handlers) startSession(w http.ResponseWriter, r *http.Request, user *store.User) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}

	expires := time.Now().Add(h.sessionTTL)
	if err := h.store.CreateSession(r.Context(), auth.HashToken(token), user.ID, expires); err != nil {
		return err
	}

	h.setSessionCookie(w, token, expires)
	return nil
}

// sendVerification makes a fresh link and emails it. A failure to send is
// logged rather than raised: the account exists either way, and the banner on
// the site offers to try again.
//
// The context is detached from the request, because the answer does not wait for
// the mail server: if the visitor's browser gives up first, the email should
// still go.
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

// register makes an account from the site itself. It is always a subscriber, and
// an address on the site's own domain is refused with an answer that says no
// more than that the account could not be made, so the reply cannot be used to
// map our own mailboxes.
func (h *handlers) register(w http.ResponseWriter, r *http.Request) {
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "accounts cannot be created right now",
		})
		return
	}

	var body registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
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
			"error": "that does not look like an email address",
		})
		return
	}
	if auth.OnReservedDomain(email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "that account could not be created",
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
			"error": "that email address is already registered",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// One less step for the visitor, and the banner on the site keeps asking for
	// the address to be confirmed until it is.
	if err := h.startSession(w, r, user); err != nil {
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a token is required"})
		return
	}

	hash := auth.HashToken(body.Token)
	user, err := h.store.UserByUserToken(r.Context(), store.UserTokenVerify, hash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "this link is not valid any more",
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
			"error": "this link is not valid any more",
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
			"error": "the confirmation email cannot be sent right now",
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
			"error": "password resets are not available right now",
		})
		return
	}

	var body forgotRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	email := auth.NormalizeEmail(body.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "that does not look like an email address",
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a token is required"})
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
			"error": "this link is not valid any more",
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
			"error": "this link is not valid any more",
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
