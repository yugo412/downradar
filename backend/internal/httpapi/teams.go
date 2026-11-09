package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

// A team invitation is a link in an inbox, so it lives a week: long enough for
// somebody to notice it, short enough that a forgotten one is not a way in.
const teamInviteTTL = 7 * 24 * time.Hour

type teamNameRequest struct {
	Name string `json:"name"`
}

type teamInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type teamMemberRequest struct {
	Role string `json:"role"`
}

// teamTransferRequest is the two ways an owner hands a team on: to somebody who
// is already a manager or member, or to an address that receives an invitation
// and becomes the owner when it accepts.
type teamTransferRequest struct {
	UserID *int64 `json:"userId"`
	Email  string `json:"email"`
}

type teamInviteRegisterRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// teamName cleans and checks what a team is called. A name is the only thing a
// person sees before they are a member, so it has to be present and short.
func teamName(raw string) (string, string) {
	name := clean(sanitize(raw))
	if name == "" {
		return "", "A team name is required"
	}
	if len([]rune(name)) > 80 {
		return "", "That team name is too long"
	}
	return name, ""
}

// invitationRole is the role an invitation may hand out. Ownership is not
// invited: it is transferred, which is a separate flow.
func invitationRole(raw string) (string, string) {
	role := strings.ToLower(strings.TrimSpace(raw))
	switch role {
	case store.TeamRoleManager, store.TeamRoleMember:
		return role, ""
	default:
		return "", "The role must be manager or member"
	}
}

// teamAccess resolves the team slug in the path and the caller's role in it. A
// team the caller does not belong to is answered as missing, so the endpoint
// does not say which teams exist.
func (h *handlers) teamAccess(w http.ResponseWriter, r *http.Request) (*store.Team, bool) {
	slug := strings.TrimSpace(r.PathValue("team"))
	if slug == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Team not found"})
		return nil, false
	}

	team, err := h.store.GetTeamBySlug(r.Context(), slug, h.user(r).ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Team not found"})
		return nil, false
	}
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return team, true
}

func (h *handlers) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.store.TeamsForUser(r.Context(), h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	invitations, err := h.store.TeamInvitationsForEmail(r.Context(), h.user(r).Email)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// A team screen offers its alerts beside the person's own, so the count and
	// the cap travel with the membership list rather than needing a read per
	// team from the page.
	views := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		used, err := h.store.WatchedTeamServiceCount(r.Context(), team.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		views = append(views, map[string]any{
			"id":         team.ID,
			"slug":       team.Slug,
			"name":       team.Name,
			"createdBy":  team.CreatedBy,
			"createdAt":  team.CreatedAt,
			"role":       team.Role,
			"alertLimit": h.teamAlertLimit,
			"alertUsed":  used,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"teams":       views,
		"limit":       h.teamLimit,
		"invitations": invitations,
	})
}

func (h *handlers) createTeam(w http.ResponseWriter, r *http.Request) {
	var body teamNameRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	name, problem := teamName(body.Name)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	count, err := h.store.TeamCountForUser(r.Context(), h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if count >= h.teamLimit {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Your account has reached its team limit"})
		return
	}

	team, err := h.store.CreateTeam(r.Context(), h.user(r).ID, name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"team": team})
}

func (h *handlers) getTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}

	members, err := h.store.TeamMembers(r.Context(), team.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	invitations := []store.TeamInvitation{}
	if team.Role == store.TeamRoleOwner || team.Role == store.TeamRoleManager {
		invitations, err = h.store.TeamInvitations(r.Context(), team.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"team":        team,
		"members":     members,
		"invitations": invitations,
	})
}

func (h *handlers) updateTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if team.Role != store.TeamRoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the team owner can change its settings"})
		return
	}

	var body teamNameRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	name, problem := teamName(body.Name)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if err := h.store.UpdateTeamName(r.Context(), team.ID, name); err != nil {
		h.fail(w, r, err)
		return
	}

	updated, err := h.store.GetTeam(r.Context(), team.ID, h.user(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": updated})
}

func (h *handlers) deleteTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if team.Role != store.TeamRoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the team owner can delete it"})
		return
	}
	if err := h.store.DeleteTeam(r.Context(), team.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyTeamInvite puts the same invitation in the account's feed. The link is
// in the email rather than the notice, so the feed never holds a token that can
// join a team. Only a sent email earns a notice: a failed send would otherwise
// point at an inbox that has nothing in it.
func (h *handlers) notifyTeamInvite(ctx context.Context, inviter, invitee *store.User, team *store.Team, invitation *store.TeamInvitation) {
	_, err := h.store.CreateNotification(ctx, store.NotificationInput{
		Audience:   store.AudienceSubscribers,
		Recipients: []int64{invitee.ID},
		Title:      fmt.Sprintf("You were invited to join %s", team.Name),
		Body: fmt.Sprintf("%s invited you to join %s as the %s. Open Teams to accept it, or use the link in your email.",
			inviter.Name, team.Name, invitation.Role),
		CreatedBy: &inviter.ID,
		DedupeKey: fmt.Sprintf("team-invite:%d", invitation.ID),
	})
	if err != nil {
		h.log.Error("could not put the team invitation in the feed", "team", team.Name,
			"user", invitee.Email, "error", err)
	}
}

// sendTeamInvite hands one message to the account mailer. A failed send is not
// hidden: the invitation stays and can be sent again, and the answer says the
// mail did not go.
func (h *handlers) sendTeamInvite(ctx context.Context, invitation *store.TeamInvitation, name, inviter, team, token string) bool {
	if h.accountMailer == nil {
		return false
	}
	if err := h.accountMailer.TeamInvite(ctx, invitation.Email, name, inviter, team, invitation.Role, token); err != nil {
		h.log.Error("could not send the team invitation", "team", team, "email", invitation.Email, "error", err)
		return false
	}
	return true
}

func (h *handlers) inviteToTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if team.Role != store.TeamRoleOwner && team.Role != store.TeamRoleManager {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "A member cannot invite people"})
		return
	}
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Invitations cannot be sent right now"})
		return
	}

	var body teamInviteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	email := auth.NormalizeEmail(body.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That does not look like an email address"})
		return
	}
	inviteRole, problem := invitationRole(body.Role)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if team.Role == store.TeamRoleManager && inviteRole != store.TeamRoleMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "A manager can only add members"})
		return
	}

	// The address need not have an account: the invitation link is how a new
	// person makes one. An existing account is also found so the notice and the
	// greeting can name the person it belongs to.
	var invitee *store.User
	found, err := h.store.UserByEmail(r.Context(), email)
	if err == nil {
		invitee = found
	} else if !errors.Is(err, store.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	name := "there"
	if invitee != nil {
		name = invitee.Name
	} else if auth.OnReservedDomain(email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "That account could not be created"})
		return
	}

	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	invitation, err := h.store.CreateTeamInvitation(r.Context(), store.TeamInvitationInput{
		TeamID:    team.ID,
		Email:     email,
		Role:      inviteRole,
		InvitedBy: h.user(r).ID,
		ExpiresAt: time.Now().Add(teamInviteTTL),
	}, auth.HashToken(token))
	if errors.Is(err, store.ErrTeamPermission) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role cannot send that invitation"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	sent := h.sendTeamInvite(r.Context(), invitation, name, h.user(r).Name, team.Name, token)
	if sent && invitee != nil {
		h.notifyTeamInvite(r.Context(), h.user(r), invitee, team, invitation)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invitation": invitation, "sent": sent})
}

// transferTeam hands ownership on. An existing manager or member takes it at
// once; an address that has no account yet is invited as the owner, and the old
// owner keeps the team until the invitation is accepted. The account deletion
// screen is what needs this: a person cannot leave a team without an owner.
func (h *handlers) transferTeam(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if team.Role != store.TeamRoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the team owner can transfer it"})
		return
	}

	var body teamTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	owner := h.user(r)

	if body.UserID != nil {
		// The old owner steps down to manager; the team keeps an owner at every
		// moment, which is what the partial unique index enforces.
		err := h.store.TransferTeamOwnership(r.Context(), team.ID, owner.ID, *body.UserID, store.TeamRoleManager)
		switch {
		case errors.Is(err, store.ErrTeamOwner):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "That member cannot take the team over"})
		case errors.Is(err, store.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Member not found"})
		case err != nil:
			h.fail(w, r, err)
		default:
			h.log.Info("team ownership transferred", "team", team.Name,
				"from", owner.Email, "to", *body.UserID)
			writeJSON(w, http.StatusOK, map[string]bool{"transferred": true})
		}
		return
	}

	email := auth.NormalizeEmail(body.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Choose a member or enter an email address",
		})
		return
	}
	if strings.EqualFold(email, owner.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "That is your own address",
		})
		return
	}
	if auth.OnReservedDomain(email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "That account could not be created",
		})
		return
	}
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Invitations cannot be sent right now",
		})
		return
	}

	// An existing account is found only so the notice and the greeting can name
	// the person; a new address makes its account from the link.
	var invitee *store.User
	if found, err := h.store.UserByEmail(r.Context(), email); err == nil {
		invitee = found
	} else if !errors.Is(err, store.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	name := "there"
	if invitee != nil {
		name = invitee.Name
	}

	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	invitation, err := h.store.CreateTeamInvitation(r.Context(), store.TeamInvitationInput{
		TeamID:    team.ID,
		Email:     email,
		Role:      store.TeamRoleOwner,
		InvitedBy: owner.ID,
		ExpiresAt: time.Now().Add(teamInviteTTL),
		Transfer:  true,
	}, auth.HashToken(token))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	sent := h.sendTeamInvite(r.Context(), invitation, name, owner.Name, team.Name, token)
	if sent && invitee != nil {
		h.notifyTeamInvite(r.Context(), owner, invitee, team, invitation)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invitation": invitation, "sent": sent})
}

func (h *handlers) resendTeamInvitation(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if h.accountMailer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Invitations cannot be sent right now"})
		return
	}

	inviteID, err := strconv.ParseInt(r.PathValue("invite"), 10, 64)
	if err != nil || inviteID <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Invitation not found"})
		return
	}
	invitation, err := h.store.TeamInvitationByID(r.Context(), team.ID, inviteID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Invitation not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if team.Role != store.TeamRoleOwner && !(team.Role == store.TeamRoleManager && invitation.Role == store.TeamRoleMember) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role cannot resend that invitation"})
		return
	}
	name := invitation.Email
	var invitee *store.User
	if found, err := h.store.UserByEmail(r.Context(), invitation.Email); err == nil {
		invitee = found
		name = found.Name
	}

	token, err := auth.NewToken()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	renewed, err := h.store.CreateTeamInvitation(r.Context(), store.TeamInvitationInput{
		TeamID:    team.ID,
		Email:     invitation.Email,
		Role:      invitation.Role,
		InvitedBy: h.user(r).ID,
		ExpiresAt: time.Now().Add(teamInviteTTL),
		Transfer:  invitation.Transfer,
	}, auth.HashToken(token))
	if err != nil {
		h.fail(w, r, err)
		return
	}

	sent := h.sendTeamInvite(r.Context(), renewed, name, h.user(r).Name, team.Name, token)
	if sent && invitee != nil {
		h.notifyTeamInvite(r.Context(), h.user(r), invitee, team, renewed)
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitation": renewed, "sent": sent})
}

func (h *handlers) revokeTeamInvitation(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}

	inviteID, err := strconv.ParseInt(r.PathValue("invite"), 10, 64)
	if err != nil || inviteID <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Invitation not found"})
		return
	}
	invitation, err := h.store.TeamInvitationByID(r.Context(), team.ID, inviteID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Invitation not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if team.Role != store.TeamRoleOwner && !(team.Role == store.TeamRoleManager && invitation.Role == store.TeamRoleMember) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Your role cannot revoke that invitation"})
		return
	}

	if err := h.store.RevokeTeamInvitation(r.Context(), team.ID, inviteID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) updateTeamMember(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}
	if team.Role != store.TeamRoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Only the team owner can change roles"})
		return
	}

	targetID, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil || targetID <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Member not found"})
		return
	}
	var body teamMemberRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	nextRole, problem := invitationRole(body.Role)
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	err = h.store.SetTeamMemberRole(r.Context(), team.ID, targetID, nextRole)
	switch {
	case errors.Is(err, store.ErrTeamOwner):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The owner cannot be demoted here"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Member not found"})
	case err != nil:
		h.fail(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handlers) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	team, ok := h.teamAccess(w, r)
	if !ok {
		return
	}

	targetID, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil || targetID <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Member not found"})
		return
	}
	targetRole, err := h.store.TeamRoleFor(r.Context(), team.ID, targetID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Member not found"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if targetRole == store.TeamRoleOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "The owner cannot be removed"})
		return
	}
	if team.Role == store.TeamRoleManager && targetRole != store.TeamRoleMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "A manager can only remove members"})
		return
	}
	if team.Role == store.TeamRoleMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "A member cannot remove people"})
		return
	}

	if err := h.store.RemoveTeamMember(r.Context(), team.ID, targetID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// registerFromTeamInvitation is the new-address path: the invitation link is
// proof of the address, so it creates a verified subscriber and spends the
// invitation in one flow. An address that already has an account is sent to the
// sign in path instead.
func (h *handlers) registerFromTeamInvitation(w http.ResponseWriter, r *http.Request) {
	var body teamInviteRegisterRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	token := r.PathValue("token")
	tokenHash := auth.HashToken(token)
	invitation, err := h.store.TeamInvitationByToken(r.Context(), tokenHash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if invitation.HasAccount {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "That address already has an account. Sign in to accept the invitation.",
		})
		return
	}

	name := clean(sanitize(body.Name))
	if problem := validateDisplayName(name); problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if auth.OnReservedDomain(invitation.Email) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "That account could not be created"})
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
	user, err := h.store.RegisterUser(r.Context(), invitation.Email, name, hash)
	if errors.Is(err, store.ErrEmailTaken) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "That address already has an account. Sign in to accept the invitation.",
		})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// The link arrived in this address's inbox, which is the same proof a
	// confirmation link gives. A failure to record it is not fatal: the banner
	// will ask for the address to be confirmed.
	if err := h.store.SetUserVerified(r.Context(), user.ID); err != nil {
		h.log.Error("could not mark the invited account verified", "user", user.Email, "error", err)
	}

	team, err := h.store.AcceptTeamInvitation(r.Context(), tokenHash, user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.startSession(w, r, user, true); err != nil {
		h.fail(w, r, err)
		return
	}

	h.log.Info("account made from a team invitation", "user", user.Email, "team", team.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"user": user, "team": team})
}

// teamInvitation is the public view of a link, enough for the join page to say
// which team it is for and who was asked. The token is the whole secret.
func (h *handlers) teamInvitation(w http.ResponseWriter, r *http.Request) {
	invitation, err := h.store.TeamInvitationByToken(r.Context(), auth.HashToken(r.PathValue("token")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitation": invitation})
}

// canJoinTeam is the check both accept paths share: the invitation has to be
// for the signed in address, and a new membership has to fit under the limit.
func (h *handlers) canJoinTeam(w http.ResponseWriter, r *http.Request, invitation *store.TeamInvitationView) bool {
	if !strings.EqualFold(invitation.Email, h.user(r).Email) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This invitation is for a different address"})
		return false
	}

	// Joining a new team counts against the same limit as making one; accepting
	// a change of role in a team already held does not.
	if _, err := h.store.TeamRoleFor(r.Context(), invitation.TeamID, h.user(r).ID); errors.Is(err, store.ErrNotFound) {
		count, countErr := h.store.TeamCountForUser(r.Context(), h.user(r).ID)
		if countErr != nil {
			h.fail(w, r, countErr)
			return false
		}
		if count >= h.teamLimit {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "You are already in the maximum number of teams"})
			return false
		}
	} else if err != nil {
		h.fail(w, r, err)
		return false
	}
	return true
}

func (h *handlers) acceptTeamInvitation(w http.ResponseWriter, r *http.Request) {
	tokenHash := auth.HashToken(r.PathValue("token"))
	invitation, err := h.store.TeamInvitationByToken(r.Context(), tokenHash)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.canJoinTeam(w, r, invitation) {
		return
	}

	team, err := h.store.AcceptTeamInvitation(r.Context(), tokenHash, h.user(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
	case errors.Is(err, store.ErrTeamInvitationMail):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This invitation is for a different address"})
	case err != nil:
		h.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"team": team})
	}
}

// acceptTeamInvitationFromInbox is the same accept for somebody who found the
// invitation on the team page rather than in their mail. The session already
// proves the address, so no token travels with it.
func (h *handlers) acceptTeamInvitationFromInbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}
	if body.ID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "An invitation id is required"})
		return
	}

	invitation, err := h.store.TeamInvitationForUser(r.Context(), body.ID, h.user(r).ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !h.canJoinTeam(w, r, invitation) {
		return
	}

	team, err := h.store.AcceptTeamInvitationByID(r.Context(), body.ID, h.user(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "This invitation is no longer available"})
	case errors.Is(err, store.ErrTeamInvitationMail):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This invitation is for a different address"})
	case err != nil:
		h.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"team": team})
	}
}
