package httpapi

import (
	"errors"
	"net/http"
	"sort"

	"downradar.info/backend/internal/store"
)

// loginEntry is one provider on the account screen: every configured provider,
// with what this account has linked to it.
type loginEntry struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Connected bool   `json:"connected"`
	Email     string `json:"email,omitempty"`
	Username  string `json:"username,omitempty"`
}

// listLogins is what the account screen reads. It lists the providers this
// deployment offers, because an unconfigured provider is not a login that can be
// used or connected.
func (h *handlers) listLogins(w http.ResponseWriter, r *http.Request) {
	current := h.user(r)

	linked, err := h.store.UserProviders(r.Context(), current.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	byProvider := make(map[string]store.LinkedProvider, len(linked))
	for _, link := range linked {
		byProvider[link.Provider] = link
	}

	list := make([]loginEntry, 0, len(h.providers))
	for _, p := range h.providers {
		entry := loginEntry{Name: p.Name(), Label: p.Label()}
		if link, ok := byProvider[p.Name()]; ok {
			entry.Connected = true
			entry.Email = link.Email
			entry.Username = link.Username
		}
		list = append(list, entry)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{
		"hasPassword": current.HasPassword,
		"providers":   list,
	})
}

// unlinkLogin takes one provider off the signed in account. The store refuses to
// remove the last login from an account that has no password.
func (h *handlers) unlinkLogin(w http.ResponseWriter, r *http.Request) {
	current := h.user(r)
	provider := r.PathValue("provider")

	err := h.store.UnlinkProvider(r.Context(), current.ID, provider)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "that login is not connected",
		})
	case errors.Is(err, store.ErrLastSignIn):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "set a password before removing the only login",
		})
	case err != nil:
		h.fail(w, r, err)
	default:
		h.log.Info("provider unlinked", "provider", provider, "user", current.Email)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
