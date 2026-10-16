package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

func (h *handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if users == nil {
		users = []store.UserView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users": users,
		"count": len(users),
	})
}

type userPayload struct {
	Name     *string `json:"name"`
	Email    *string `json:"email"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

func (h *handlers) findUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return nil, false
	}

	user, err := h.store.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return nil, false
	}
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return user, true
}

func (h *handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	target, ok := h.findUser(w, r)
	if !ok {
		return
	}

	var body userPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Name == nil && body.Email == nil && body.Role == nil && body.Disabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to update"})
		return
	}

	actor := h.user(r)

	name := target.Name
	email := target.Email
	role := target.Role
	disabled := target.Disabled()

	if body.Name != nil {
		name = clean(sanitize(*body.Name))
		if problem := validateDisplayName(name); problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
	}
	if body.Email != nil {
		email = auth.NormalizeEmail(*body.Email)
		if !auth.ValidEmail(email) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "that does not look like an email address",
			})
			return
		}
	}
	if body.Role != nil {
		role = strings.ToLower(clean(sanitize(*body.Role)))
		if !auth.ValidRole(role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "role must be one of " + strings.Join(auth.Roles(), ", "),
			})
			return
		}
	}
	if body.Disabled != nil {
		disabled = *body.Disabled
	}

	// Losing the last admin, or locking yourself out, leaves nobody able to
	// manage anything. Both are refused rather than merely warned about.
	if target.Role == auth.RoleAdmin && !target.Disabled() && (role != auth.RoleAdmin || disabled) {
		if target.ID == actor.ID {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "you cannot disable or demote your own account",
			})
			return
		}
		admins, err := h.store.CountEnabledAdmins(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if admins <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "this is the last active admin",
			})
			return
		}
	}

	if name != target.Name {
		if err := h.store.SetUserName(r.Context(), target.ID, name); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if email != target.Email {
		if err := h.store.SetUserEmail(r.Context(), target.ID, email); err != nil {
			if errors.Is(err, store.ErrEmailTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": email + " is already used by another account",
				})
				return
			}
			h.fail(w, r, err)
			return
		}
	}
	if role != target.Role {
		if err := h.store.SetUserRole(r.Context(), target.ID, role); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if disabled != target.Disabled() {
		if err := h.store.SetUserDisabled(r.Context(), target.ID, disabled); err != nil {
			h.fail(w, r, err)
			return
		}
		if disabled {
			// The session check already refuses a disabled account; ending the
			// rows as well keeps the table honest.
			if _, err := h.store.DeleteUserSessions(r.Context(), target.ID); err != nil {
				h.fail(w, r, err)
				return
			}
		}
	}

	updated, err := h.store.UserByID(r.Context(), target.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("user updated",
		"id", updated.ID, "role", updated.Role, "disabled", updated.Disabled(), "by", actor.Email)
	writeJSON(w, http.StatusOK, map[string]any{"user": updated})
}

func (h *handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	target, ok := h.findUser(w, r)
	if !ok {
		return
	}

	actor := h.user(r)
	if target.ID == actor.ID {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "you cannot delete your own account",
		})
		return
	}
	if target.Role == auth.RoleAdmin && !target.Disabled() {
		admins, err := h.store.CountEnabledAdmins(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if admins <= 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "this is the last active admin",
			})
			return
		}
	}

	if err := h.store.DeleteUser(r.Context(), target.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		h.fail(w, r, err)
		return
	}

	h.log.Info("user deleted", "id", target.ID, "email", target.Email, "by", actor.Email)
	writeJSON(w, http.StatusOK, map[string]any{"id": target.ID})
}
