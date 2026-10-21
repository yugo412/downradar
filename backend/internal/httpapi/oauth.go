package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/oauth"
	"downradar.info/backend/internal/store"
)

// The cookie that carries the state of a sign in through a provider. It is short
// lived and read only by the callback: the provider sends the state back in the
// query string, and the two have to agree for the answer to be the one this
// browser asked for.
const (
	oauthStateCookie = "dr_oauth"
	oauthStateTTL    = 10 * time.Minute
	oauthCookiePath  = "/api/v1/auth"
)

// errProviderAddress is a sign in that cannot become an account: the provider
// gave no usable address, or gave one the site does not create accounts for.
var errProviderAddress = errors.New("httpapi: the provider address cannot be used")

// oauthRedirectURL is the address registered with the provider. It is the public
// site rather than the API's own address, because the person's browser follows it
// and because the session cookie has to be set on the site's domain.
func (h *handlers) oauthRedirectURL(provider string) string {
	return strings.TrimSuffix(h.publicURL, "/") + "/api/v1/auth/" + provider + "/callback"
}

// authProviders is what the sign in screens ask for. A provider without
// credentials is not in the map, so its button is not offered, the same way the
// site does not offer to email a link while there is no mail server.
func (h *handlers) authProviders(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	list := make([]entry, 0, len(h.providers))
	for _, p := range h.providers {
		list = append(list, entry{Name: p.Name(), Label: p.Label()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"providers": list})
}

// startOAuth sends the browser to the provider. It is a navigation rather than a
// fetch, so everything it needs to remember travels in a cookie.
func (h *handlers) startOAuth(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	provider, ok := h.providers[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "That sign in provider is not configured",
		})
		return
	}

	state, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.setOAuthState(w, state, localPath(r.URL.Query().Get("next")))

	http.Redirect(w, r, provider.AuthorizeURL(state, h.oauthRedirectURL(name)), http.StatusFound)
}

// oauthCallback takes the answer back. Everything that can go wrong ends on the
// sign in screen with a reason in the address, because the visitor arrived by
// following a link and a JSON answer would be a dead end.
func (h *handlers) oauthCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	provider, ok := h.providers[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "That sign in provider is not configured",
		})
		return
	}

	query := r.URL.Query()
	state, next, ok := h.takeOAuthState(w, r)
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(query.Get("state"))) != 1 {
		h.log.Warn("sign in callback did not come from this browser",
			"provider", name, "ip", h.clientIP(r))
		h.oauthFailed(w, r, name, "failed")
		return
	}

	// The provider reports a refusal, such as the person pressing cancel, in the
	// query string rather than by failing the request.
	if refusal := query.Get("error"); refusal != "" {
		h.log.Info("sign in refused at the provider", "provider", name, "error", refusal)
		h.oauthFailed(w, r, name, "refused")
		return
	}

	code := query.Get("code")
	if code == "" {
		h.oauthFailed(w, r, name, "failed")
		return
	}

	identity, err := provider.Exchange(r.Context(), code, h.oauthRedirectURL(name))
	if err != nil {
		h.log.Warn("sign in exchange failed", "provider", name, "error", err)
		h.oauthFailed(w, r, name, "failed")
		return
	}

	user, err := h.accountFromProvider(r.Context(), name, identity)
	switch {
	case errors.Is(err, errProviderAddress), errors.Is(err, store.ErrProviderTaken):
		h.log.Warn("sign in with a provider refused", "provider", name, "error", err)
		h.oauthFailed(w, r, name, "failed")
		return
	case err != nil:
		h.log.Error("sign in with a provider failed", "provider", name, "error", err)
		h.oauthFailed(w, r, name, "failed")
		return
	}

	if user.Disabled() {
		h.log.Warn("sign in refused, account disabled", "provider", name, "user", user.Email)
		h.oauthFailed(w, r, name, "disabled")
		return
	}

	if err := h.startSession(w, r, user, true); err != nil {
		h.fail(w, r, err)
		return
	}

	if next = localPath(next); next == "" {
		next = "/"
	}
	h.log.Info("signed in with a provider", "provider", name, "user", user.Email, "role", user.Role)
	http.Redirect(w, r, next, http.StatusFound)
}

// accountFromProvider finds or makes the account behind an identity. The
// provider's own id comes first, then the address, and only then a new account:
// that way an address somebody already registered gets the new way in instead of
// a second account.
func (h *handlers) accountFromProvider(ctx context.Context, provider string, id oauth.Identity) (*store.User, error) {
	email := auth.NormalizeEmail(id.Email)
	if !auth.ValidEmail(email) {
		return nil, fmt.Errorf("%w: %q is not an address", errProviderAddress, id.Email)
	}

	user, err := h.store.UserByProvider(ctx, provider, id.Subject)
	switch {
	case err == nil:
		// A disabled account is answered as it is: the handler refuses it, and
		// nothing about it should change on the way to that answer.
		if user.Disabled() {
			return user, nil
		}
		if err := h.store.TouchProvider(ctx, provider, id.Subject); err != nil {
			return nil, err
		}
		return user, nil
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}

	user, err = h.store.UserByEmail(ctx, email)
	switch {
	case err == nil:
		if user.Disabled() {
			return user, nil
		}
		if err := h.store.LinkProvider(ctx, user.ID, provider, id.Subject, email, id.Username); err != nil {
			return nil, err
		}
		if user.Verified() {
			return user, nil
		}
		// The provider proved the address, so the account is confirmed now.
		if err := h.store.SetUserVerified(ctx, user.ID); err != nil {
			return nil, err
		}
		return h.store.UserByID(ctx, user.ID)
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}

	// Nobody has the address yet, so this is a registration. An address on this
	// site's own domain is refused the way the form refuses it.
	if auth.OnReservedDomain(email) {
		return nil, fmt.Errorf("%w: the address is on this site's own domain", errProviderAddress)
	}

	return h.store.CreateProviderUser(ctx, provider, id.Subject, email,
		providerDisplayName(id), email, id.Username)
}

// providerDisplayName is what the new account is called. A provider may have no
// display name, and the one it has may be too short or too long for the account
// screens, so the username and then the address have a turn.
func providerDisplayName(id oauth.Identity) string {
	for _, candidate := range []string{id.Name, id.Username, id.Email} {
		name := clean(sanitize(candidate))
		if candidate == id.Email {
			if at := strings.Index(name, "@"); at > 0 {
				name = name[:at]
			}
		}
		if validateDisplayName(name) == "" {
			return name
		}
	}
	return "Visitor"
}

// oauthFailed sends the browser to the sign in screen with the reason and the
// provider in the address, which is where the form can name both.
func (h *handlers) oauthFailed(w http.ResponseWriter, r *http.Request, provider, reason string) {
	query := url.Values{"oauth": {reason}, "provider": {provider}}
	http.Redirect(w, r, "/login?"+query.Encode(), http.StatusFound)
}

// setOAuthState puts the state and the page to open afterwards in a cookie. The
// page is kept here rather than in the provider's own state so the callback
// cannot be sent anywhere but a path on this site.
func (h *handlers) setOAuthState(w http.ResponseWriter, state, next string) {
	value := state + ":" + base64.RawURLEncoding.EncodeToString([]byte(next))
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    value,
		Path:     oauthCookiePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// takeOAuthState reads and then spends the cookie: it is good for one callback,
// whatever the answer turns out to be.
func (h *handlers) takeOAuthState(w http.ResponseWriter, r *http.Request) (state, next string, ok bool) {
	cookie, err := r.Cookie(oauthStateCookie)
	h.clearOAuthState(w)
	if err != nil {
		return "", "", false
	}

	raw, encoded, found := strings.Cut(cookie.Value, ":")
	if !found {
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	return raw, string(decoded), true
}

func (h *handlers) clearOAuthState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     oauthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.sessionSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
