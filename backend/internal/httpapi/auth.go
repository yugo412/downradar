package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

type contextKey string

const userContextKey contextKey = "user"

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Remember is the visitor's answer to whether the browser should keep the
	// session after it closes. Left out means no.
	Remember bool `json:"remember"`
}

func (h *handlers) user(r *http.Request) *store.User {
	u, _ := r.Context().Value(userContextKey).(*store.User)
	return u
}

func (h *handlers) sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(h.sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (h *handlers) authenticate(r *http.Request) (*store.User, error) {
	token := h.sessionToken(r)
	if token == "" {
		return nil, store.ErrNotFound
	}
	return h.store.SessionUser(r.Context(), auth.HashToken(token))
}

// setSessionCookie hands the browser its session. Without remember me the cookie
// lasts only while the browser is open, though the row behind it still expires on
// its own schedule; closing the window ends this machine's copy.
func (h *handlers) setSessionCookie(w http.ResponseWriter, token string, expires time.Time, remember bool) {
	cookie := &http.Cookie{
		Name:     h.sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	}
	if remember {
		cookie.Expires = expires
		cookie.MaxAge = int(time.Until(expires).Seconds())
	}
	http.SetCookie(w, cookie)
}

func (h *handlers) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// checkOrigin refuses a state changing request that a browser sent from another
// site. The session cookie is SameSite=Lax, which already keeps it away from
// cross site POSTs, so this is the second lock on the same door.
func (h *handlers) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Host, h.originHost(r)) {
		h.log.Warn("refused a cross site request", "origin", origin, "host", h.originHost(r), "path", r.URL.Path)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Cross site request refused"})
		return false
	}
	return true
}

// originHost is the host the browser used. Requests arrive through the Nitro
// proxy, so r.Host is the API's own address; the visitor's host travels in
// X-Forwarded-Host, trusted only from the proxy like the client IP.
func (h *handlers) originHost(r *http.Request) string {
	if h.isInternal(r) {
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if host := strings.TrimSpace(first); host != "" {
				return host
			}
		}
	}
	return r.Host
}

// authEndpoint marks a response as private: none of it is ever cacheable.
func (h *handlers) authEndpoint(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !h.checkOrigin(w, r) {
			return
		}
		next(w, r)
	}
}

// limited throttles by client IP. skipInternal is for the endpoints the site's
// own proxy calls on every page load.
func (h *handlers) limited(limiter *ipLimiter, skipInternal bool, message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if skipInternal && h.isInternal(r) {
			next(w, r)
			return
		}

		ip := h.clientIP(r)
		if !limiter.allow(ip) {
			h.log.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
			w.Header().Set("Retry-After", strconv.Itoa(limiter.retryAfterSeconds()))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": message})
			return
		}
		next(w, r)
	}
}

// requireAuth asks for a session, no matter the role, which is what the account
// screens need: everybody has a profile and a password.
func (h *handlers) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return h.authEndpoint(func(w http.ResponseWriter, r *http.Request) {
		user, err := h.authenticate(r)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Sign in required"})
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

func (h *handlers) requireRole(roles []string, next http.HandlerFunc) http.HandlerFunc {
	return h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user := h.user(r)
		if !slices.Contains(roles, user.Role) {
			h.log.Warn("role refused", "user", user.Email, "role", user.Role, "path", r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role cannot do this"})
			return
		}
		next(w, r)
	})
}

// requireStaff asks for a session whose role may look after the catalogue. A
// subscriber may add and edit their own service, but staff-only actions such as
// detection stay here.
func (h *handlers) requireStaff(next http.HandlerFunc) http.HandlerFunc {
	return h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user := h.user(r)
		if !auth.CanManageServices(user.Role) {
			h.log.Warn("role refused", "user", user.Email, "role", user.Role, "path", r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role cannot do this"})
			return
		}
		next(w, r)
	})
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	email := auth.NormalizeEmail(body.Email)
	user, err := h.store.UserByEmail(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		auth.CheckPassword(h.dummyHash, body.Password)
		h.log.Warn("failed sign in", "email", email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid email or password"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if !auth.CheckPassword(user.PasswordHash, body.Password) {
		h.log.Warn("failed sign in", "email", email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid email or password"})
		return
	}

	// A disabled account gets the same answer as a wrong password, so the screen
	// does not reveal which accounts exist.
	if user.Disabled() {
		h.log.Warn("sign in refused, account disabled", "email", email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid email or password"})
		return
	}

	if err := h.startSession(w, r, user, body.Remember); err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("signed in", "user", user.Email, "role", user.Role, "remember", body.Remember)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	if token := h.sessionToken(r); token != "" {
		if err := h.store.DeleteSession(r.Context(), auth.HashToken(token)); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"user": nil})
}

// me answers with a null user rather than a 401, because the page renders this
// on every visit and a signed out visitor is the normal case, not an error.
func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.authenticate(r)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

type profileRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func validateDisplayName(name string) string {
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "Name must be between 2 and 80 characters"
	}
	return ""
}

// updateProfile changes the name, and the address when one is given. A new
// address has not been confirmed by anybody, so the confirmation is taken away
// and a fresh link goes to it.
func (h *handlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body profileRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	current := h.user(r)
	name := clean(sanitize(body.Name))
	email := auth.NormalizeEmail(body.Email)

	if problem := validateDisplayName(name); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if email == "" {
		email = current.Email
	}
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That does not look like an email address",
		})
		return
	}
	if auth.OnReservedDomain(email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "That address cannot be used",
		})
		return
	}

	if err := h.store.SetUserName(r.Context(), current.ID, name); err != nil {
		h.fail(w, r, err)
		return
	}

	if email != current.Email {
		if err := h.store.SetUserEmail(r.Context(), current.ID, email); err != nil {
			if errors.Is(err, store.ErrEmailTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "That email address is already registered",
				})
				return
			}
			h.fail(w, r, err)
			return
		}

		updated, err := h.store.UserByID(r.Context(), current.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.sendVerification(context.WithoutCancel(r.Context()), updated)
		h.log.Info("address changed", "user", current.Email, "to", email)
	}

	updated, err := h.store.UserByEmail(r.Context(), email)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("profile updated", "user", updated.Email)
	writeJSON(w, http.StatusOK, map[string]any{"user": updated})
}

type passwordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	var body passwordRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	current := h.user(r)

	// A provider account has no password yet, so the session is proof enough to set
	// the first one. An account that has one must give it, or a borrowed browser
	// could take it over.
	if current.PasswordHash != "" && !auth.CheckPassword(current.PasswordHash, body.CurrentPassword) {
		h.log.Warn("password change refused", "user", current.Email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "The current password is not correct"})
		return
	}
	if err := auth.ValidatePassword(body.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.store.SetUserPassword(r.Context(), current.ID, hash); err != nil {
		h.fail(w, r, err)
		return
	}

	// Every other session is ended; the browser doing the change keeps its own,
	// so nobody is thrown out of the page they are standing on.
	ended := int64(0)
	if token := h.sessionToken(r); token != "" {
		ended, err = h.store.DeleteOtherSessions(r.Context(), current.ID, auth.HashToken(token))
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}

	h.log.Info("password changed", "user", current.Email, "other_sessions_ended", ended)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "otherSessionsEnded": ended})
}

type deleteAccountRequest struct {
	Password string `json:"password"`
}

// deleteAccount removes the account and everything the cascade takes with it.
// Staff are refused because the site needs somebody to manage it, and a provider
// account has no password to confirm with until it sets one.
func (h *handlers) deleteAccount(w http.ResponseWriter, r *http.Request) {
	current := h.user(r)
	if auth.CanManageServices(current.Role) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "staff accounts cannot delete themselves",
		})
		return
	}

	var body deleteAccountRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if current.PasswordHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "set a password before deleting this account",
		})
		return
	}
	if !auth.CheckPassword(current.PasswordHash, body.Password) {
		h.log.Warn("account deletion refused", "user", current.Email, "ip", h.clientIP(r))
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "the password is not correct",
		})
		return
	}

	if err := h.store.DeleteUser(r.Context(), current.ID); err != nil {
		h.fail(w, r, err)
		return
	}

	h.clearSessionCookie(w)
	h.log.Info("account deleted", "id", current.ID, "user", current.Email)

	// The account is already gone, so a mail server that refuses the last
	// message cannot undo the deletion. Tell the log and let the answer stand.
	if h.accountMailer != nil {
		ctx := context.WithoutCancel(r.Context())
		if err := h.accountMailer.Deleted(ctx, current.Email, current.Name); err != nil {
			h.log.Error("could not send the account deletion email", "user", current.Email, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
