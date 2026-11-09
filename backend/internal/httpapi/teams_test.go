package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"downradar.info/backend/internal/auth"
	"downradar.info/backend/internal/store"
)

type teamAnswer struct {
	Team        store.Team             `json:"team"`
	Members     []store.TeamMember     `json:"members"`
	Invitations []store.TeamInvitation `json:"invitations"`
}

type inviteAnswer struct {
	Invitation store.TeamInvitation `json:"invitation"`
	Sent       bool                 `json:"sent"`
}

type teamsAnswer struct {
	Teams       []store.Team               `json:"teams"`
	Limit       int                        `json:"limit"`
	Invitations []store.TeamInvitationView `json:"invitations"`
}

func decodeTeam(t *testing.T, body []byte) teamAnswer {
	t.Helper()
	var answer teamAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode team answer: %v (%s)", err, body)
	}
	return answer
}

func decodeInvite(t *testing.T, body []byte) inviteAnswer {
	t.Helper()
	var answer inviteAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("decode invite answer: %v (%s)", err, body)
	}
	return answer
}

func teamPath(slug string) string {
	return "/api/v1/teams/" + slug
}

func memberPath(teamSlug string, userID int64) string {
	return teamPath(teamSlug) + "/members/" + strconv.FormatInt(userID, 10)
}

// A subscriber makes a team, invites an existing account, and the invited
// person accepts with the link from the mail. The whole lifecycle is exercised
// through the same handlers the screens use.
func TestATeamLifecycleThroughTheAPI(t *testing.T) {
	st, router, spy := testRouterWithMail(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	manager := testUser(t, st, auth.RoleSubscriber)
	member := testUser(t, st, auth.RoleSubscriber)
	invitee := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	managerCookie := sessionFor(t, st, manager)
	memberCookie := sessionFor(t, st, member)
	inviteeCookie := sessionFor(t, st, invitee)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Acme"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeTeam(t, rec.Body.Bytes())
	teamID := created.Team.ID
	teamSlug := created.Team.Slug
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), teamID) })
	if created.Team.Name != "Acme" || created.Team.Role != store.TeamRoleOwner {
		t.Fatalf("created team = %+v", created.Team)
	}

	rec = call(t, router, http.MethodGet, teamPath(teamSlug), "", ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get team = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeTeam(t, rec.Body.Bytes())
	if len(got.Members) != 1 || got.Members[0].Role != store.TeamRoleOwner {
		t.Errorf("members = %+v, want the owner", got.Members)
	}

	// Somebody outside the team cannot see it.
	if rec := call(t, router, http.MethodGet, teamPath(teamSlug), "", memberCookie); rec.Code != http.StatusNotFound {
		t.Errorf("a non-member read the team = %d, want 404", rec.Code)
	}

	// Put a manager and a member in place directly so the role rules can be
	// exercised without spending three invitations first.
	if err := st.AddTeamMember(t.Context(), teamID, manager.ID, store.TeamRoleManager); err != nil {
		t.Fatalf("AddTeamMember manager: %v", err)
	}
	if err := st.AddTeamMember(t.Context(), teamID, member.ID, store.TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember member: %v", err)
	}

	// A member can see the team and its people, but not the pending invitations.
	rec = call(t, router, http.MethodGet, teamPath(teamSlug), "", memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("a member read the team = %d: %s", rec.Code, rec.Body.String())
	}
	if len(decodeTeam(t, rec.Body.Bytes()).Invitations) != 0 {
		t.Error("a member saw the pending invitations")
	}

	// The owner invites an existing account as a manager.
	rec = call(t, router, http.MethodPost, teamPath(teamSlug)+"/invitations",
		`{"email":"`+invitee.Email+`","role":"manager"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	invite := decodeInvite(t, rec.Body.Bytes())
	if invite.Invitation.Role != store.TeamRoleManager || !invite.Sent {
		t.Errorf("invitation = %+v, sent %v", invite.Invitation, invite.Sent)
	}
	sent := spy.sent()
	if len(sent) != 1 || sent[0].to != invitee.Email {
		t.Fatalf("mail = %+v, want one to the invitee", sent)
	}
	token := tokenFrom(t, sent[0].body)
	if !strings.Contains(sent[0].body, "Acme") || !strings.Contains(sent[0].body, "manager") {
		t.Errorf("the mail does not name the team and role:\n%s", sent[0].body)
	}

	// The invited account also gets a notice in its feed. The actionable link
	// stays in the email, so the feed never carries the join token.
	rec = call(t, router, http.MethodGet, "/api/v1/notifications", "", inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("notifications = %d: %s", rec.Code, rec.Body.String())
	}
	var feed struct {
		Notifications []store.NotificationView `json:"notifications"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil {
		t.Fatalf("decode notifications: %v", err)
	}
	if len(feed.Notifications) != 1 {
		t.Fatalf("notifications = %d, want the invitation", len(feed.Notifications))
	}
	notice := feed.Notifications[0]
	if !strings.Contains(notice.Title, "Acme") || !strings.Contains(notice.Body, "manager") {
		t.Errorf("notice = %+v, want the team and role", notice)
	}
	if notice.ServiceSlug != "" || notice.AlertID != nil {
		t.Errorf("notice = %+v, want no service or alert link", notice)
	}

	// The invitee sees the same invitation on their team page before they open
	// the link in their mail.
	rec = call(t, router, http.MethodGet, "/api/v1/teams", "", inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitee teams = %d: %s", rec.Code, rec.Body.String())
	}
	var invited teamsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &invited); err != nil {
		t.Fatalf("decode invitee teams: %v", err)
	}
	if len(invited.Invitations) != 1 || invited.Invitations[0].TeamName != "Acme" {
		t.Fatalf("invitations = %+v, want the Acme invitation", invited.Invitations)
	}

	// The public view names the team, and the wrong account cannot spend it.
	if rec := call(t, router, http.MethodGet, "/api/v1/teams/invitations/"+token, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("public invitation = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPost, "/api/v1/teams/invitations/"+token+"/accept", "", memberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("another address accepted = %d, want 403", rec.Code)
	}
	rec = call(t, router, http.MethodPost, "/api/v1/teams/invitations/"+token+"/accept", "", inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPost, "/api/v1/teams/invitations/"+token+"/accept", "", inviteeCookie); rec.Code != http.StatusNotFound {
		t.Errorf("the invitation worked twice = %d, want 404", rec.Code)
	}
	if role, _ := st.TeamRoleFor(t.Context(), teamID, invitee.ID); role != store.TeamRoleManager {
		t.Errorf("invitee role = %q, want manager", role)
	}

	// A manager can add a member but not another manager; a member can do neither.
	extra := testUser(t, st, auth.RoleSubscriber)
	if rec := call(t, router, http.MethodPost, teamPath(teamSlug)+"/invitations",
		`{"email":"`+extra.Email+`","role":"member"}`, managerCookie); rec.Code != http.StatusCreated {
		t.Errorf("a manager invited a member = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPost, teamPath(teamSlug)+"/invitations",
		`{"email":"`+testUser(t, st, auth.RoleSubscriber).Email+`","role":"manager"}`, managerCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a manager invited a manager = %d, want 403", rec.Code)
	}
	if rec := call(t, router, http.MethodPost, teamPath(teamSlug)+"/invitations",
		`{"email":"`+testUser(t, st, auth.RoleSubscriber).Email+`","role":"member"}`, memberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a member invited somebody = %d, want 403", rec.Code)
	}

	// Only the owner changes roles or the name.
	if rec := call(t, router, http.MethodPatch, memberPath(teamSlug, member.ID),
		`{"role":"manager"}`, managerCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a manager changed a role = %d, want 403", rec.Code)
	}
	if rec := call(t, router, http.MethodPatch, memberPath(teamSlug, member.ID),
		`{"role":"manager"}`, ownerCookie); rec.Code != http.StatusNoContent {
		t.Errorf("the owner changed a role = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPatch, memberPath(teamSlug, member.ID),
		`{"role":"member"}`, ownerCookie); rec.Code != http.StatusNoContent {
		t.Errorf("the owner changed the role back = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPatch, teamPath(teamSlug), `{"name":"Acme Two"}`, managerCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a manager renamed the team = %d, want 403", rec.Code)
	}
	if rec := call(t, router, http.MethodPatch, teamPath(teamSlug), `{"name":"Acme Two"}`, ownerCookie); rec.Code != http.StatusOK {
		t.Errorf("the owner renamed the team = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// A manager cannot remove another manager; the owner can remove a member.
	if err := st.AddTeamMember(t.Context(), teamID, invitee.ID, store.TeamRoleManager); err != nil {
		t.Fatalf("restore manager: %v", err)
	}
	if rec := call(t, router, http.MethodDelete, memberPath(teamSlug, invitee.ID), "", managerCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a manager removed a manager = %d, want 403", rec.Code)
	}
	if rec := call(t, router, http.MethodDelete, memberPath(teamSlug, member.ID), "", managerCookie); rec.Code != http.StatusNoContent {
		t.Errorf("a manager removed a member = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// The owner deletes the team.
	if rec := call(t, router, http.MethodDelete, teamPath(teamSlug), "", ownerCookie); rec.Code != http.StatusNoContent {
		t.Fatalf("delete team = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodGet, teamPath(teamSlug), "", ownerCookie); rec.Code != http.StatusNotFound {
		t.Errorf("the deleted team was readable = %d, want 404", rec.Code)
	}
}

// An invitation can be resent or revoked, and the old link stops working as soon
// as it is replaced.
func TestTeamInvitationsCanBeResentAndRevoked(t *testing.T) {
	st, router, spy := testRouterWithMail(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	invitee := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, owner)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Resend"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeTeam(t, rec.Body.Bytes())
	teamID := created.Team.ID
	teamSlug := created.Team.Slug
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), teamID) })

	rec = call(t, router, http.MethodPost, teamPath(teamSlug)+"/invitations",
		`{"email":"`+invitee.Email+`","role":"member"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	first := decodeInvite(t, rec.Body.Bytes())
	firstToken := tokenFrom(t, spy.sent()[0].body)

	rec = call(t, router, http.MethodPost,
		teamPath(teamSlug)+"/invitations/"+strconv.FormatInt(first.Invitation.ID, 10)+"/resend",
		"", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend = %d: %s", rec.Code, rec.Body.String())
	}
	renewed := decodeInvite(t, rec.Body.Bytes())
	messages := spy.sent()
	if len(messages) != 2 {
		t.Fatalf("mails = %d, want two", len(messages))
	}
	secondToken := tokenFrom(t, messages[1].body)
	if firstToken == secondToken {
		t.Fatal("the resent link kept the old token")
	}
	if rec := call(t, router, http.MethodGet, "/api/v1/teams/invitations/"+firstToken, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("the old link still works = %d, want 404", rec.Code)
	}

	if rec := call(t, router, http.MethodDelete,
		teamPath(teamSlug)+"/invitations/"+strconv.FormatInt(renewed.Invitation.ID, 10),
		"", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodGet, "/api/v1/teams/invitations/"+secondToken, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("the revoked link still works = %d, want 404", rec.Code)
	}
}

// An invitation can go to an address with no account. The link proves the
// address, so it creates a verified account and spends the invitation at once.
func TestAnInvitationCanMakeAnAccount(t *testing.T) {
	st, router, spy := testRouterWithMail(t)
	owner := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, owner)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"New Address"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeTeam(t, rec.Body.Bytes())
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), created.Team.ID) })

	email := "new-person-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	t.Cleanup(func() { dropUser(t, st, email) })

	rec = call(t, router, http.MethodPost, teamPath(created.Team.Slug)+"/invitations",
		`{"email":"`+email+`","role":"member"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	sent := spy.sent()
	if len(sent) != 1 || sent[0].to != email {
		t.Fatalf("mail = %+v, want one to the new address", sent)
	}
	token := tokenFrom(t, sent[0].body)

	// The public view tells the join page to ask for a new account.
	rec = call(t, router, http.MethodGet, "/api/v1/teams/invitations/"+token, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("public invitation = %d: %s", rec.Code, rec.Body.String())
	}
	var view struct {
		Invitation store.TeamInvitationView `json:"invitation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}
	if view.Invitation.HasAccount {
		t.Error("the invitation says an account exists before one was made")
	}

	rec = call(t, router, http.MethodPost,
		"/api/v1/teams/invitations/"+token+"/register",
		`{"name":"New Person","password":"a good password"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register from invitation = %d: %s", rec.Code, rec.Body.String())
	}
	var answer struct {
		User store.User `json:"user"`
		Team store.Team `json:"team"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if answer.User.Email != email || answer.Team.Slug != created.Team.Slug {
		t.Errorf("answer = %+v, want the new account and team", answer)
	}

	var newCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dr_session" {
			newCookie = "dr_session=" + c.Value
		}
	}
	if newCookie == "" {
		t.Fatal("registration did not sign the account in")
	}
	if rec := call(t, router, http.MethodGet, "/api/v1/auth/me", "", newCookie); rec.Code != http.StatusOK {
		t.Errorf("the new session = %d, want it signed in", rec.Code)
	}

	user, err := st.UserByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	if !user.Verified() {
		t.Error("the invited account was not marked verified")
	}
	if role, _ := st.TeamRoleFor(t.Context(), created.Team.ID, user.ID); role != store.TeamRoleMember {
		t.Errorf("role = %q, want member", role)
	}

	// The invitation was spent, so the same link cannot make another account.
	rec = call(t, router, http.MethodPost,
		"/api/v1/teams/invitations/"+token+"/register",
		`{"name":"Another Person","password":"another password"}`, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("the spent invitation made another account = %d, want 404", rec.Code)
	}
}

// An account can be in three teams. Making a fourth is refused, and so is
// accepting an invitation to one.
func TestAnAccountCanBelongToThreeTeams(t *testing.T) {
	st, router, _ := testRouterWithMail(t)
	user := testUser(t, st, auth.RoleSubscriber)
	cookie := sessionFor(t, st, user)

	for _, name := range []string{"One", "Two", "Three"} {
		rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"`+name+`"}`, cookie)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s = %d: %s", name, rec.Code, rec.Body.String())
		}
		team := decodeTeam(t, rec.Body.Bytes()).Team
		t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	}

	rec := call(t, router, http.MethodGet, "/api/v1/teams", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list teams = %d: %s", rec.Code, rec.Body.String())
	}
	var listed teamsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode teams: %v", err)
	}
	if listed.Limit != 3 || len(listed.Teams) != 3 {
		t.Fatalf("teams = %d, limit = %d, want three of three", len(listed.Teams), listed.Limit)
	}

	rec = call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Four"}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("a fourth team = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// The same ceiling applies when the fourth team comes from an invitation.
	owner := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	rec = call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Inviting"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create inviting team = %d: %s", rec.Code, rec.Body.String())
	}
	inviting := decodeTeam(t, rec.Body.Bytes()).Team
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), inviting.ID) })
	rec = call(t, router, http.MethodPost, teamPath(inviting.Slug)+"/invitations",
		`{"email":"`+user.Email+`","role":"member"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, router, http.MethodGet, "/api/v1/teams", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitee teams = %d: %s", rec.Code, rec.Body.String())
	}
	var inbox teamsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox.Invitations) != 1 {
		t.Fatalf("inbox = %+v, want the invitation", inbox.Invitations)
	}
	if rec := call(t, router, http.MethodPost, "/api/v1/teams/invitations/accept",
		`{"id":`+strconv.FormatInt(inbox.Invitations[0].ID, 10)+`}`, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("accepting at the limit = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

// An invitation can be accepted from the team page without the mail link; the
// session's address is what proves it belongs to the account.
func TestATeamInvitationCanBeAcceptedFromTheInbox(t *testing.T) {
	st, router, _ := testRouterWithMail(t)
	owner := testUser(t, st, auth.RoleSubscriber)
	invitee := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	inviteeCookie := sessionFor(t, st, invitee)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Inbox"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeTeam(t, rec.Body.Bytes())
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), created.Team.ID) })

	rec = call(t, router, http.MethodPost, teamPath(created.Team.Slug)+"/invitations",
		`{"email":"`+invitee.Email+`","role":"member"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite = %d: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/teams", "", inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitee teams = %d: %s", rec.Code, rec.Body.String())
	}
	var inbox teamsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox.Invitations) != 1 {
		t.Fatalf("inbox = %+v, want one invitation", inbox.Invitations)
	}

	rec = call(t, router, http.MethodPost, "/api/v1/teams/invitations/accept",
		`{"id":`+strconv.FormatInt(inbox.Invitations[0].ID, 10)+`}`, inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox accept = %d: %s", rec.Code, rec.Body.String())
	}
	if role, _ := st.TeamRoleFor(t.Context(), created.Team.ID, invitee.ID); role != store.TeamRoleMember {
		t.Errorf("role = %q, want member", role)
	}

	rec = call(t, router, http.MethodGet, "/api/v1/teams", "", inviteeCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("invitee teams after accept = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("decode teams after accept: %v", err)
	}
	if len(inbox.Invitations) != 0 {
		t.Errorf("the accepted invitation is still in the inbox: %+v", inbox.Invitations)
	}
}

// A team owner can put a service in a team. Every member may see it and look
// after the team's alerts, but only the owner may edit or remove the service.
func TestTeamOwnsServicesAndAlerts(t *testing.T) {
	st, router, _ := testRouterWithMail(t)

	owner := testUser(t, st, auth.RoleSubscriber)
	member := testUser(t, st, auth.RoleSubscriber)
	outsider := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	memberCookie := sessionFor(t, st, member)
	outsiderCookie := sessionFor(t, st, outsider)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Rows Team"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	team := decodeTeam(t, rec.Body.Bytes()).Team
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(t.Context(), team.ID, member.ID, store.TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	name := fmt.Sprintf("zz-team-service-%d", time.Now().UnixNano())
	body := fmt.Sprintf(`{"name":%q,"url":"https://status.example.invalid","visibility":"private","teamId":%d}`, name, team.ID)

	// A member may not add a row to the team; its owner may.
	if rec := call(t, router, http.MethodPost, "/api/v1/services", body, memberCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("a member added a team service = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, router, http.MethodPost, "/api/v1/services", body, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("team service = %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode service: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteService(context.Background(), created.Slug) })

	if rec := call(t, router, http.MethodGet, "/api/v1/services/"+created.Slug, "", memberCookie); rec.Code != http.StatusOK {
		t.Errorf("a team member read the service = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodGet, "/api/v1/services/"+created.Slug, "", outsiderCookie); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider read the private team service = %d, want 404", rec.Code)
	}
	if rec := call(t, router, http.MethodPatch, "/api/v1/services/"+created.Slug,
		fmt.Sprintf(`{"name":%q}`, name+" two"), memberCookie); rec.Code != http.StatusForbidden {
		t.Errorf("a member edited the team service = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, router, http.MethodPatch, "/api/v1/services/"+created.Slug,
		fmt.Sprintf(`{"name":%q}`, name+" two"), ownerCookie); rec.Code != http.StatusOK {
		t.Errorf("the owner edited the team service = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Any member can save and pause the team's alert, which belongs to the team
	// rather than to whoever saved it.
	vendor := alertVendor(t, st)
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+vendor+"/alerts",
		fmt.Sprintf(`{"email":"team@example.com","teamId":%d,"wholeService":true}`, team.ID), memberCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("team alert = %d: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/auth/alerts", "", ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("team alert list = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Alerts []struct {
			TeamID   *int64 `json:"teamId"`
			TeamName string `json:"teamName"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode alerts: %v", err)
	}
	found := false
	for _, alert := range list.Alerts {
		if alert.TeamID != nil && *alert.TeamID == team.ID && alert.TeamName == "Rows Team" {
			found = true
		}
	}
	if !found {
		t.Errorf("team alert is missing from the owner's list: %s", rec.Body.String())
	}

	// A webhook call made for the team is read through the team, and it does
	// not show up as a personal call to the member who made it.
	serviceID, _, err := st.PublicService(t.Context(), vendor)
	if err != nil {
		t.Fatalf("PublicService: %v", err)
	}
	if err := st.LogWebhookCall(t.Context(), nil, &team.ID, serviceID, nil, store.WebhookCall{
		Kind: store.CallTest, URL: "https://hooks.example.invalid/team", Method: http.MethodPost,
	}); err != nil {
		t.Fatalf("LogWebhookCall: %v", err)
	}

	detail := "/api/v1/vendors/" + vendor + "/alerts?team=" + strconv.FormatInt(team.ID, 10)
	rec = call(t, router, http.MethodGet, detail, "", ownerCookie)
	if rec.Code != http.StatusOK {
		t.Errorf("team alert detail = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var teamDetail struct {
		Calls []store.WebhookCall `json:"calls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &teamDetail); err != nil {
		t.Fatalf("decode team alert detail: %v", err)
	}
	if len(teamDetail.Calls) != 1 {
		t.Errorf("team calls = %d, want the call made for the team", len(teamDetail.Calls))
	}
	rec = call(t, router, http.MethodGet, "/api/v1/vendors/"+vendor+"/alerts", "", memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("personal alert detail = %d: %s", rec.Code, rec.Body.String())
	}
	var personalDetail struct {
		Calls []store.WebhookCall `json:"calls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &personalDetail); err != nil {
		t.Fatalf("decode personal alert detail: %v", err)
	}
	if len(personalDetail.Calls) != 0 {
		t.Errorf("personal calls = %d, want none of the team's", len(personalDetail.Calls))
	}
	if rec := call(t, router, http.MethodGet, detail, "", outsiderCookie); rec.Code != http.StatusNotFound {
		t.Errorf("an outsider read the team alert = %d, want 404", rec.Code)
	}
	if rec := call(t, router, http.MethodPost, "/api/v1/vendors/"+vendor+"/alerts/pause",
		fmt.Sprintf(`{"kind":"email","paused":true,"teamId":%d}`, team.ID), memberCookie); rec.Code != http.StatusNoContent {
		t.Errorf("a member paused the team alert = %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// The team's owner may remove the published service the team owns. A
	// published row is taken off the site rather than erased.
	if rec := call(t, router, http.MethodDelete, "/api/v1/services/"+created.Slug, "", ownerCookie); rec.Code != http.StatusOK {
		t.Errorf("the owner removed the team service = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// A team is held to its own alert count, not to the personal count of whoever
// saves the alert, and the membership list carries the team's count back.
func TestTeamAlertLimitIsTheTeamsOwn(t *testing.T) {
	st, router, _ := testRouterWithOptions(t, func(opts *Options) {
		opts.AlertLimitPerTeam = 1
	})

	owner := testUser(t, st, auth.RoleSubscriber)
	member := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	memberCookie := sessionFor(t, st, member)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Limited Team"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	team := decodeTeam(t, rec.Body.Bytes()).Team
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(t.Context(), team.ID, member.ID, store.TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	first := alertVendor(t, st)
	second := alertVendor(t, st)

	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+first+"/alerts",
		fmt.Sprintf(`{"email":"team-one@example.com","teamId":%d,"wholeService":true}`, team.ID), ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first team alert = %d: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+second+"/alerts",
		fmt.Sprintf(`{"email":"team-two@example.com","teamId":%d,"wholeService":true}`, team.ID), memberCookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("second team alert = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// The same member's personal row is counted against their own cap, so the
	// team's full list does not stop it.
	rec = call(t, router, http.MethodPost, "/api/v1/vendors/"+second+"/alerts",
		`{"email":"member@example.com","wholeService":true}`, memberCookie)
	if rec.Code != http.StatusCreated {
		t.Errorf("a personal alert beside the team's = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodGet, "/api/v1/teams", "", ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list teams = %d: %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Teams []struct {
			ID         int64 `json:"id"`
			AlertLimit int   `json:"alertLimit"`
			AlertUsed  int   `json:"alertUsed"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode teams: %v", err)
	}
	if len(listed.Teams) != 1 {
		t.Fatalf("teams = %d, want 1", len(listed.Teams))
	}
	if got := listed.Teams[0]; got.ID != team.ID || got.AlertLimit != 1 || got.AlertUsed != 1 {
		t.Errorf("team = %+v, want id %d, limit 1, used 1", got, team.ID)
	}
}

// An owner who is leaving hands the team to a member first. While they still
// own it, account deletion is refused.
func TestAnOwnerHandsTheTeamOnBeforeDeletingTheAccount(t *testing.T) {
	st, router, _ := testRouterWithMail(t)

	owner := testUserWithPassword(t, st, auth.RoleSubscriber, "owner password")
	member := testUser(t, st, auth.RoleSubscriber)
	ownerCookie := sessionFor(t, st, owner)
	memberCookie := sessionFor(t, st, member)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"Handover"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	team := decodeTeam(t, rec.Body.Bytes()).Team
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(t.Context(), team.ID, member.ID, store.TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	rec = call(t, router, http.MethodDelete, "/api/v1/auth/account", `{"password":"owner password"}`, ownerCookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting an owner's account = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Handover") {
		t.Errorf("the refusal does not name the team: %s", rec.Body.String())
	}

	rec = call(t, router, http.MethodPost, teamPath(team.Slug)+"/transfer",
		fmt.Sprintf(`{"userId":%d}`, member.ID), ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer to a member = %d: %s", rec.Code, rec.Body.String())
	}
	if role, err := st.TeamRoleFor(t.Context(), team.ID, owner.ID); err != nil || role != store.TeamRoleManager {
		t.Errorf("the old owner's role = %q (%v), want manager", role, err)
	}
	if role, err := st.TeamRoleFor(t.Context(), team.ID, member.ID); err != nil || role != store.TeamRoleOwner {
		t.Errorf("the new owner's role = %q (%v), want owner", role, err)
	}

	rec = call(t, router, http.MethodGet, teamPath(team.Slug), "", memberCookie)
	if rec.Code != http.StatusOK {
		t.Errorf("the new owner reading the team = %d: %s", rec.Code, rec.Body.String())
	}

	rec = call(t, router, http.MethodDelete, "/api/v1/auth/account", `{"password":"owner password"}`, ownerCookie)
	if rec.Code != http.StatusOK {
		t.Errorf("the old owner deleting the account = %d: %s", rec.Code, rec.Body.String())
	}
}

// The other way out is an invitation to a new address. It is an owner
// invitation, so accepting it steps the old owner down, and asking again
// replaces the pending link.
func TestAnOwnerInvitesANewAddressToTakeTheTeam(t *testing.T) {
	st, router, spy := testRouterWithMail(t)

	owner := testUserWithPassword(t, st, auth.RoleSubscriber, "owner password")
	ownerCookie := sessionFor(t, st, owner)

	rec := call(t, router, http.MethodPost, "/api/v1/teams", `{"name":"New Owner Team"}`, ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create team = %d: %s", rec.Code, rec.Body.String())
	}
	team := decodeTeam(t, rec.Body.Bytes()).Team
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })

	address := fmt.Sprintf("zz-next-owner-%d@example.com", time.Now().UnixNano())
	rec = call(t, router, http.MethodPost, teamPath(team.Slug)+"/transfer",
		fmt.Sprintf(`{"email":%q}`, address), ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("transfer invitation = %d: %s", rec.Code, rec.Body.String())
	}
	invite := decodeInvite(t, rec.Body.Bytes())
	if invite.Invitation.Role != store.TeamRoleOwner || !invite.Invitation.Transfer {
		t.Errorf("invitation = %+v, want an owner transfer", invite.Invitation)
	}
	if !invite.Sent {
		t.Error("the transfer invitation did not go out")
	}

	// The owner still runs the team until the invitation is accepted.
	if rec := call(t, router, http.MethodDelete, "/api/v1/auth/account", `{"password":"owner password"}`, ownerCookie); rec.Code != http.StatusConflict {
		t.Errorf("deleting before the invitation is accepted = %d, want 409", rec.Code)
	}

	rec = call(t, router, http.MethodPost, teamPath(team.Slug)+"/transfer",
		fmt.Sprintf(`{"email":%q}`, address), ownerCookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("replacing the transfer invitation = %d: %s", rec.Code, rec.Body.String())
	}
	replaced := decodeInvite(t, rec.Body.Bytes())
	if replaced.Invitation.ID == invite.Invitation.ID {
		t.Error("the replacement kept the old invitation")
	}
	if got := len(spy.sent()); got < 2 {
		t.Errorf("messages sent = %d, want the invitation and its replacement", got)
	}

	// The other choice is deleting the team and everything it owns.
	if rec := call(t, router, http.MethodDelete, teamPath(team.Slug), "", ownerCookie); rec.Code != http.StatusNoContent {
		t.Errorf("deleting the team = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, router, http.MethodDelete, "/api/v1/auth/account", `{"password":"owner password"}`, ownerCookie)
	if rec.Code != http.StatusOK {
		t.Errorf("deleting the account after the team = %d: %s", rec.Code, rec.Body.String())
	}
}
