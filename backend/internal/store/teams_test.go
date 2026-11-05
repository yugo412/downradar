package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A team is made with its creator as the only owner. The role table is what the
// rest of the code reads, and the owner cannot be removed or demoted directly.
func TestACreatedTeamHasOneOwner(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	creator := newTestUser(t, st, "subscriber")
	other := newTestUser(t, st, "subscriber")

	team, err := st.CreateTeam(ctx, creator.ID, "Acme")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if team.Role != TeamRoleOwner {
		t.Errorf("the creator's role = %q, want owner", team.Role)
	}

	got, err := st.GetTeam(ctx, team.ID, creator.ID)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.Name != "Acme" || got.Role != TeamRoleOwner {
		t.Errorf("team = %+v, want Acme with the owner", got)
	}
	if _, err := st.GetTeam(ctx, team.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a non-member saw the team: %v", err)
	}
	teams, err := st.TeamsForUser(ctx, creator.ID)
	if err != nil {
		t.Fatalf("TeamsForUser: %v", err)
	}
	if len(teams) == 0 || teams[0].ID != team.ID {
		t.Errorf("teams = %+v, want the new team", teams)
	}

	members, err := st.TeamMembers(ctx, team.ID)
	if err != nil {
		t.Fatalf("TeamMembers: %v", err)
	}
	if len(members) != 1 || members[0].UserID != creator.ID || members[0].Role != TeamRoleOwner {
		t.Errorf("members = %+v, want the creator as the only owner", members)
	}

	if err := st.AddTeamMember(ctx, team.ID, other.ID, TeamRoleManager); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}
	if err := st.AddTeamMember(ctx, team.ID, creator.ID, TeamRoleMember); !errors.Is(err, ErrTeamOwner) {
		t.Errorf("demoting the owner through AddTeamMember = %v, want ErrTeamOwner", err)
	}
	if err := st.SetTeamMemberRole(ctx, team.ID, creator.ID, TeamRoleMember); !errors.Is(err, ErrTeamOwner) {
		t.Errorf("SetTeamMemberRole on the owner = %v, want ErrTeamOwner", err)
	}
	if err := st.RemoveTeamMember(ctx, team.ID, creator.ID); !errors.Is(err, ErrTeamOwner) {
		t.Errorf("removing the owner = %v, want ErrTeamOwner", err)
	}
	if err := st.AddTeamMember(ctx, team.ID, other.ID, TeamRoleOwner); !errors.Is(err, ErrTeamRole) {
		t.Errorf("adding an owner = %v, want ErrTeamRole", err)
	}

	// The database itself keeps a second owner out, not just the helper.
	if _, err := st.pool.Exec(ctx, `
		insert into team_members (team_id, user_id, role) values ($1, $2, 'owner')`,
		team.ID, other.ID); err == nil {
		t.Error("a second owner was accepted by the database")
	}
}

func TestOwnershipIsTransferredNotShared(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	manager := newTestUser(t, st, "subscriber")
	team, err := st.CreateTeam(ctx, owner.ID, "Transfer")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(ctx, team.ID, manager.ID, TeamRoleManager); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	if err := st.TransferTeamOwnership(ctx, team.ID, manager.ID, owner.ID, TeamRoleManager); !errors.Is(err, ErrTeamOwner) {
		t.Errorf("a manager transferred the team = %v, want ErrTeamOwner", err)
	}
	if err := st.TransferTeamOwnership(ctx, team.ID, owner.ID, manager.ID, TeamRoleManager); err != nil {
		t.Fatalf("TransferTeamOwnership: %v", err)
	}

	if role, err := st.TeamRoleFor(ctx, team.ID, manager.ID); err != nil || role != TeamRoleOwner {
		t.Errorf("new owner role = %q, %v", role, err)
	}
	if role, err := st.TeamRoleFor(ctx, team.ID, owner.ID); err != nil || role != TeamRoleManager {
		t.Errorf("old owner role = %q, %v", role, err)
	}

	// Exactly one owner survives the move.
	var owners int
	if err := st.pool.QueryRow(ctx, `
		select count(*) from team_members where team_id = $1 and role = 'owner'`,
		team.ID).Scan(&owners); err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if owners != 1 {
		t.Errorf("owners = %d, want one", owners)
	}
}

func TestTeamInvitationsAreSingleUseAndForTheAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	invitee := newTestUser(t, st, "subscriber")
	outsider := newTestUser(t, st, "subscriber")
	team, err := st.CreateTeam(ctx, owner.ID, "Invites")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })

	token := testTokenHash("team-invite-one")
	invitation, err := st.CreateTeamInvitation(ctx, TeamInvitationInput{
		TeamID:    team.ID,
		Email:     invitee.Email,
		Role:      TeamRoleManager,
		InvitedBy: owner.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}, token)
	if err != nil {
		t.Fatalf("CreateTeamInvitation: %v", err)
	}
	if invitation.Role != TeamRoleManager || invitation.Transfer {
		t.Errorf("invitation = %+v, want a manager invitation", invitation)
	}

	view, err := st.TeamInvitationByToken(ctx, token)
	if err != nil {
		t.Fatalf("TeamInvitationByToken: %v", err)
	}
	if view.TeamName != "Invites" || view.Email != invitee.Email {
		t.Errorf("invitation view = %+v", view)
	}

	if _, err := st.AcceptTeamInvitation(ctx, token, outsider.ID); !errors.Is(err, ErrTeamInvitationMail) {
		t.Errorf("accepting another address's invitation = %v, want ErrTeamInvitationMail", err)
	}
	if _, err := st.AcceptTeamInvitation(ctx, token, invitee.ID); err != nil {
		t.Fatalf("AcceptTeamInvitation: %v", err)
	}
	if role, err := st.TeamRoleFor(ctx, team.ID, invitee.ID); err != nil || role != TeamRoleManager {
		t.Errorf("invitee role = %q, %v, want manager", role, err)
	}
	if _, err := st.AcceptTeamInvitation(ctx, token, invitee.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second acceptance = %v, want ErrNotFound", err)
	}

	// Asking again replaces the old pending link rather than leaving two live.
	oldToken := testTokenHash("team-invite-old")
	if _, err := st.CreateTeamInvitation(ctx, TeamInvitationInput{
		TeamID: team.ID, Email: outsider.Email, Role: TeamRoleMember,
		InvitedBy: owner.ID, ExpiresAt: time.Now().Add(time.Hour),
	}, oldToken); err != nil {
		t.Fatalf("first invitation: %v", err)
	}
	newToken := testTokenHash("team-invite-new")
	if _, err := st.CreateTeamInvitation(ctx, TeamInvitationInput{
		TeamID: team.ID, Email: outsider.Email, Role: TeamRoleMember,
		InvitedBy: owner.ID, ExpiresAt: time.Now().Add(time.Hour),
	}, newToken); err != nil {
		t.Fatalf("replacement invitation: %v", err)
	}
	if _, err := st.TeamInvitationByToken(ctx, oldToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("the replaced link still works: %v", err)
	}
	if _, err := st.TeamInvitationByToken(ctx, newToken); err != nil {
		t.Errorf("the replacement link does not work: %v", err)
	}
}

func TestTeamInvitationRolesFollowTheInviter(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	manager := newTestUser(t, st, "subscriber")
	member := newTestUser(t, st, "subscriber")
	target := newTestUser(t, st, "subscriber")
	team, err := st.CreateTeam(ctx, owner.ID, "Roles")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(ctx, team.ID, manager.ID, TeamRoleManager); err != nil {
		t.Fatalf("AddTeamMember manager: %v", err)
	}
	if err := st.AddTeamMember(ctx, team.ID, member.ID, TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember member: %v", err)
	}

	invite := func(by int64, role string, transfer bool, token string) error {
		_, err := st.CreateTeamInvitation(ctx, TeamInvitationInput{
			TeamID: team.ID, Email: target.Email, Role: role,
			InvitedBy: by, ExpiresAt: time.Now().Add(time.Hour), Transfer: transfer,
		}, testTokenHash(token))
		return err
	}

	if err := invite(manager.ID, TeamRoleManager, false, "manager-invites-manager"); !errors.Is(err, ErrTeamPermission) {
		t.Errorf("a manager invited a manager = %v, want ErrTeamPermission", err)
	}
	if err := invite(member.ID, TeamRoleMember, false, "member-invites"); !errors.Is(err, ErrTeamPermission) {
		t.Errorf("a member invited somebody = %v, want ErrTeamPermission", err)
	}
	if err := invite(manager.ID, TeamRoleMember, false, "manager-invites-member"); err != nil {
		t.Errorf("a manager invited a member = %v, want it allowed", err)
	}
	if err := invite(owner.ID, TeamRoleOwner, true, "owner-invites-owner"); err != nil {
		t.Errorf("the owner invited a new owner = %v, want it allowed", err)
	}
	if err := invite(owner.ID, TeamRoleOwner, false, "owner-not-transfer"); !errors.Is(err, ErrTeamInvitation) {
		t.Errorf("an owner invitation without a transfer = %v, want ErrTeamInvitation", err)
	}
}

func TestAnOwnerInvitationTransfersTheTeam(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	next := newTestUser(t, st, "subscriber")
	team, err := st.CreateTeam(ctx, owner.ID, "Handover")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if err := st.AddTeamMember(ctx, team.ID, next.ID, TeamRoleMember); err != nil {
		t.Fatalf("AddTeamMember: %v", err)
	}

	token := testTokenHash("owner-transfer")
	if _, err := st.CreateTeamInvitation(ctx, TeamInvitationInput{
		TeamID: team.ID, Email: next.Email, Role: TeamRoleOwner,
		InvitedBy: owner.ID, ExpiresAt: time.Now().Add(time.Hour), Transfer: true,
	}, token); err != nil {
		t.Fatalf("CreateTeamInvitation: %v", err)
	}
	if _, err := st.AcceptTeamInvitation(ctx, token, next.ID); err != nil {
		t.Fatalf("AcceptTeamInvitation: %v", err)
	}

	if role, _ := st.TeamRoleFor(ctx, team.ID, next.ID); role != TeamRoleOwner {
		t.Errorf("new owner role = %q, want owner", role)
	}
	if role, _ := st.TeamRoleFor(ctx, team.ID, owner.ID); role != TeamRoleManager {
		t.Errorf("old owner role = %q, want manager", role)
	}
}

func TestTeamOwnedRowsStayExclusive(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()

	owner := newTestUser(t, st, "subscriber")
	team, err := st.CreateTeam(ctx, owner.ID, "Rows")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })

	slug, err := st.CreateService(ctx, NewService{
		Name:                  uniqueName("team-service"),
		URL:                   "https://status.example.com",
		Profile:               "statuspage-v2",
		PollIntervalSeconds:   300,
		ActiveIntervalSeconds: 60,
		TeamID:                &team.ID,
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}
	t.Cleanup(func() { dropServices(context.Background(), st, slug) })

	var teamID, serviceOwner *int64
	if err := st.pool.QueryRow(ctx, `
		select team_id, owner_id from services where slug = $1`, slug,
	).Scan(&teamID, &serviceOwner); err != nil {
		t.Fatalf("read service owner: %v", err)
	}
	if teamID == nil || *teamID != team.ID || serviceOwner != nil {
		t.Errorf("service owners = team %v, user %v, want only the team", teamID, serviceOwner)
	}
	if _, err := st.CreateService(ctx, NewService{
		Name: uniqueName("two-owners"), URL: "https://status.example.com",
		Profile: "statuspage-v2", OwnerID: &owner.ID, TeamID: &team.ID,
	}); err == nil {
		t.Error("a service with both owners was accepted")
	}

	serviceID := testService(t, st, uniqueName("team-alert"))
	email := "team@example.com"
	if err := st.SubscribeAlert(ctx, serviceID, nil, AlertChange{
		TeamID: &team.ID, Email: &email, EmailToken: "team-token", Targets: WholeVendor,
	}); err != nil {
		t.Fatalf("SubscribeAlert: %v", err)
	}
	var subscriptionTeam, subscriptionUser *int64
	if err := st.pool.QueryRow(ctx, `
		select team_id, user_id from alert_subscriptions where service_id = $1`,
		serviceID).Scan(&subscriptionTeam, &subscriptionUser); err != nil {
		t.Fatalf("read alert owner: %v", err)
	}
	if subscriptionTeam == nil || *subscriptionTeam != team.ID || subscriptionUser != nil {
		t.Errorf("alert owners = team %v, user %v, want only the team", subscriptionTeam, subscriptionUser)
	}

	otherEmail := "other@example.com"
	if err := st.SubscribeAlert(ctx, serviceID, &owner.ID, AlertChange{
		TeamID: &team.ID, Email: &otherEmail, EmailToken: "other-token", Targets: WholeVendor,
	}); err == nil {
		t.Error("an alert with both owners was accepted")
	}
}

func TestATeamCannotTakeTheJoinPageAddress(t *testing.T) {
	st := integrationStore(t)
	ctx := context.Background()
	owner := newTestUser(t, st, "subscriber")

	team, err := st.CreateTeam(ctx, owner.ID, "Join")
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteTeam(context.Background(), team.ID) })
	if team.Slug == "join" || team.Slug == "" {
		t.Errorf("slug = %q, want an address that is not the join page", team.Slug)
	}
}
