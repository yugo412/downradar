package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"downradar.info/backend/internal/auth"
)

// The roles a team member can hold. The creator is the only owner; ownership is
// transferred, not shared, so a team always has one person who can hand it on.
const (
	TeamRoleOwner   = "owner"
	TeamRoleManager = "manager"
	TeamRoleMember  = "member"
)

func ValidTeamRole(role string) bool {
	switch role {
	case TeamRoleOwner, TeamRoleManager, TeamRoleMember:
		return true
	default:
		return false
	}
}

// CanManageTeam is the gate for settings and deletion.
func CanManageTeam(role string) bool {
	return role == TeamRoleOwner
}

// CanManageTeamMembers covers invitations. A manager looks after member
// requests; only the owner can promote somebody or change the owner.
func CanManageTeamMembers(role string) bool {
	return role == TeamRoleOwner || role == TeamRoleManager
}

// CanManageTeamAlerts is what every member has: the team's alerts are the
// team's, so a member can look after them.
func CanManageTeamAlerts(role string) bool {
	return ValidTeamRole(role)
}

var (
	ErrTeamName           = errors.New("store: a team needs a name")
	ErrTeamRole           = errors.New("store: invalid team role")
	ErrTeamPermission     = errors.New("store: this team role cannot do that")
	ErrTeamOwner          = errors.New("store: the team owner cannot be changed this way")
	ErrTeamInvitation     = errors.New("store: that invitation cannot be used")
	ErrTeamInvitationMail = errors.New("store: the invitation belongs to another address")
)

// Team is one workspace. Role is the requesting person's role in it and is
// filled by the reads that are asked for on somebody's behalf.
type Team struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedBy *int64    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Role      string    `json:"role,omitempty"`
}

type TeamMember struct {
	UserID    int64     `json:"userId"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

type TeamInvitation struct {
	ID         int64      `json:"id"`
	TeamID     int64      `json:"teamId"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Transfer   bool       `json:"transfer,omitempty"`
	InvitedBy  *int64     `json:"invitedBy,omitempty"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	AcceptedAt *time.Time `json:"acceptedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type TeamInvitationView struct {
	TeamInvitation
	TeamName      string `json:"teamName"`
	InvitedByName string `json:"invitedByName,omitempty"`
}

type TeamInvitationInput struct {
	TeamID    int64
	Email     string
	Role      string
	InvitedBy int64
	ExpiresAt time.Time
	Transfer  bool
}

const teamColumns = `t.id, t.name, t.created_by, t.created_at`

// CreateTeam makes the team and its owner in one transaction, so a team is never
// left without the one person who can manage it.
func (s *Store) CreateTeam(ctx context.Context, creatorID int64, name string) (*Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrTeamName
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("teams: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	team := Team{Role: TeamRoleOwner}
	err = tx.QueryRow(ctx, `
		insert into teams (name, created_by) values ($1, $2)
		returning id, name, created_by, created_at`, name, creatorID,
	).Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("teams: create: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		insert into team_members (team_id, user_id, role) values ($1, $2, 'owner')`,
		team.ID, creatorID); err != nil {
		return nil, fmt.Errorf("teams: add the owner: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("teams: commit: %w", err)
	}
	return &team, nil
}

// GetTeam returns one team only if the person is a member of it.
func (s *Store) GetTeam(ctx context.Context, teamID, userID int64) (*Team, error) {
	var team Team
	err := s.pool.QueryRow(ctx, `
		select `+teamColumns+`, m.role
		from teams t
		join team_members m on m.team_id = t.id
		where t.id = $1 and m.user_id = $2`, teamID, userID,
	).Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt, &team.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("teams: get %d: %w", teamID, err)
	}
	return &team, nil
}

// TeamsForUser is the switch's list: every team the person belongs to, with the
// role they hold in each.
func (s *Store) TeamsForUser(ctx context.Context, userID int64) ([]Team, error) {
	rows, err := s.pool.Query(ctx, `
		select `+teamColumns+`, m.role
		from teams t
		join team_members m on m.team_id = t.id
		where m.user_id = $1
		order by t.name, t.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("teams: list for user: %w", err)
	}
	defer rows.Close()

	out := []Team{}
	for rows.Next() {
		var team Team
		if err := rows.Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt, &team.Role); err != nil {
			return nil, fmt.Errorf("teams: scan: %w", err)
		}
		out = append(out, team)
	}
	return out, rows.Err()
}

// TeamRoleFor is the authorization read: what the role table says this person
// may do in the team. A missing membership is ErrNotFound.
func (s *Store) TeamRoleFor(ctx context.Context, teamID, userID int64) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `
		select role from team_members where team_id = $1 and user_id = $2`,
		teamID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("teams: role: %w", err)
	}
	return role, nil
}

func (s *Store) TeamMembers(ctx context.Context, teamID int64) ([]TeamMember, error) {
	rows, err := s.pool.Query(ctx, `
		select m.user_id, u.email, u.name, m.role, m.created_at
		from team_members m
		join users u on u.id = m.user_id
		where m.team_id = $1
		order by case m.role when 'owner' then 0 when 'manager' then 1 else 2 end,
		         u.name, u.email`, teamID)
	if err != nil {
		return nil, fmt.Errorf("teams: members: %w", err)
	}
	defer rows.Close()

	out := []TeamMember{}
	for rows.Next() {
		var member TeamMember
		if err := rows.Scan(&member.UserID, &member.Email, &member.Name,
			&member.Role, &member.CreatedAt); err != nil {
			return nil, fmt.Errorf("teams: scan member: %w", err)
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

// AddTeamMember adds a manager or a member. An owner is only ever made by
// transferring ownership, so the owner role is refused here.
func (s *Store) AddTeamMember(ctx context.Context, teamID, userID int64, role string) error {
	if role != TeamRoleManager && role != TeamRoleMember {
		return ErrTeamRole
	}

	tag, err := s.pool.Exec(ctx, `
		insert into team_members (team_id, user_id, role) values ($1, $2, $3)
		on conflict (team_id, user_id) do update set role = excluded.role
		where team_members.role <> 'owner'`, teamID, userID, role)
	if err != nil {
		return fmt.Errorf("teams: add member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTeamOwner
	}
	return nil
}

// SetTeamMemberRole changes a manager or member. The owner is moved by
// TransferTeamOwnership, which keeps the one-owner rule intact.
func (s *Store) SetTeamMemberRole(ctx context.Context, teamID, userID int64, role string) error {
	if role != TeamRoleManager && role != TeamRoleMember {
		return ErrTeamRole
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("teams: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	err = tx.QueryRow(ctx, `
		select role from team_members where team_id = $1 and user_id = $2 for update`,
		teamID, userID).Scan(&current)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("teams: read member: %w", err)
	case current == TeamRoleOwner:
		return ErrTeamOwner
	}

	if _, err := tx.Exec(ctx, `
		update team_members set role = $3 where team_id = $1 and user_id = $2`,
		teamID, userID, role); err != nil {
		return fmt.Errorf("teams: set role: %w", err)
	}
	return tx.Commit(ctx)
}

// RemoveTeamMember drops a manager or member. The owner has to transfer
// ownership first, so a team cannot be left without one.
func (s *Store) RemoveTeamMember(ctx context.Context, teamID, userID int64) error {
	tag, err := s.pool.Exec(ctx, `
		delete from team_members
		where team_id = $1 and user_id = $2 and role <> 'owner'`, teamID, userID)
	if err != nil {
		return fmt.Errorf("teams: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, roleErr := s.TeamRoleFor(ctx, teamID, userID); roleErr == nil {
			return ErrTeamOwner
		}
		return ErrNotFound
	}
	return nil
}

// TransferTeamOwnership hands the team to an existing manager or member and
// gives the old owner the role the caller chose, so ownership stays with one
// person throughout.
func (s *Store) TransferTeamOwnership(ctx context.Context, teamID, fromUserID, toUserID int64, oldRole string) error {
	if oldRole != TeamRoleManager && oldRole != TeamRoleMember {
		return ErrTeamRole
	}
	if fromUserID == toUserID {
		return ErrTeamOwner
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("teams: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fromRole, toRole string
	if err := tx.QueryRow(ctx, `
		select role from team_members where team_id = $1 and user_id = $2 for update`,
		teamID, fromUserID).Scan(&fromRole); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("teams: read the owner: %w", err)
	}
	if fromRole != TeamRoleOwner {
		return ErrTeamOwner
	}
	if err := tx.QueryRow(ctx, `
		select role from team_members where team_id = $1 and user_id = $2 for update`,
		teamID, toUserID).Scan(&toRole); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("teams: read the new owner: %w", err)
	}
	if toRole == TeamRoleOwner {
		return ErrTeamOwner
	}

	// Step down first: the partial unique index allows only one owner at a time.
	if _, err := tx.Exec(ctx, `
		update team_members set role = $3 where team_id = $1 and user_id = $2`,
		teamID, fromUserID, oldRole); err != nil {
		return fmt.Errorf("teams: step the owner down: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update team_members set role = 'owner' where team_id = $1 and user_id = $2`,
		teamID, toUserID); err != nil {
		return fmt.Errorf("teams: make the new owner: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateTeamName(ctx context.Context, teamID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrTeamName
	}
	tag, err := s.pool.Exec(ctx, `
		update teams set name = $2, updated_at = now() where id = $1`, teamID, name)
	if err != nil {
		return fmt.Errorf("teams: rename: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTeam removes the team and everything it owns. The cascades take the
// members, invitations, services and their alerts with it.
func (s *Store) DeleteTeam(ctx context.Context, teamID int64) error {
	tag, err := s.pool.Exec(ctx, `delete from teams where id = $1`, teamID)
	if err != nil {
		return fmt.Errorf("teams: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateTeamInvitation writes one pending link. The caller makes and sends the
// token; only its hash is kept. An address with a pending invitation gets a
// fresh one, so asking again replaces the old link rather than leaving two live.
func (s *Store) CreateTeamInvitation(ctx context.Context, in TeamInvitationInput, tokenHash string) (*TeamInvitation, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !auth.ValidEmail(email) {
		return nil, fmt.Errorf("teams: invitation address: %w", ErrTeamInvitation)
	}
	if !ValidTeamRole(in.Role) {
		return nil, ErrTeamRole
	}
	if (in.Role == TeamRoleOwner) != in.Transfer {
		return nil, ErrTeamInvitation
	}
	if in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("teams: invitation expiry: %w", ErrTeamInvitation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("teams: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inviterRole string
	err = tx.QueryRow(ctx, `
		select role from team_members where team_id = $1 and user_id = $2 for update`,
		in.TeamID, in.InvitedBy).Scan(&inviterRole)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrTeamPermission
	case err != nil:
		return nil, fmt.Errorf("teams: read the inviter: %w", err)
	}
	switch {
	case inviterRole == TeamRoleOwner:
	case inviterRole == TeamRoleManager && in.Role == TeamRoleMember:
	default:
		return nil, ErrTeamPermission
	}

	// A manager cannot replace an owner or another manager's invitation. The
	// owner can replace anything, which is how a transfer is resent.
	var pendingRole string
	err = tx.QueryRow(ctx, `
		select role from team_invitations
		where team_id = $1 and lower(email) = $2 and accepted_at is null
		order by id desc limit 1`, in.TeamID, email).Scan(&pendingRole)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("teams: read the pending invitation: %w", err)
	case inviterRole == TeamRoleManager && pendingRole != TeamRoleMember:
		return nil, ErrTeamPermission
	}

	if _, err := tx.Exec(ctx, `
		delete from team_invitations
		where team_id = $1 and lower(email) = $2 and accepted_at is null`,
		in.TeamID, email); err != nil {
		return nil, fmt.Errorf("teams: replace the invitation: %w", err)
	}

	invitation := TeamInvitation{
		TeamID:    in.TeamID,
		Email:     email,
		Role:      in.Role,
		Transfer:  in.Transfer,
		InvitedBy: &in.InvitedBy,
		ExpiresAt: in.ExpiresAt,
	}
	err = tx.QueryRow(ctx, `
		insert into team_invitations
			(team_id, email, role, token_hash, invited_by, transfer, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7)
		returning id, created_at`,
		in.TeamID, email, in.Role, tokenHash, in.InvitedBy, in.Transfer, in.ExpiresAt,
	).Scan(&invitation.ID, &invitation.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("teams: create invitation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("teams: commit: %w", err)
	}
	return &invitation, nil
}

// TeamInvitations is the pending list for the members screen. Expired rows stay
// until they are replaced or revoked, so the screen can say that one ran out.
func (s *Store) TeamInvitations(ctx context.Context, teamID int64) ([]TeamInvitation, error) {
	rows, err := s.pool.Query(ctx, `
		select id, team_id, email, role, transfer, invited_by, expires_at, accepted_at, created_at
		from team_invitations
		where team_id = $1 and accepted_at is null
		order by created_at desc, id desc`, teamID)
	if err != nil {
		return nil, fmt.Errorf("teams: invitations: %w", err)
	}
	defer rows.Close()

	out := []TeamInvitation{}
	for rows.Next() {
		var invitation TeamInvitation
		if err := rows.Scan(&invitation.ID, &invitation.TeamID, &invitation.Email,
			&invitation.Role, &invitation.Transfer, &invitation.InvitedBy,
			&invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.CreatedAt); err != nil {
			return nil, fmt.Errorf("teams: scan invitation: %w", err)
		}
		out = append(out, invitation)
	}
	return out, rows.Err()
}

// TeamInvitationByToken resolves a live invitation. An expired or accepted link
// is not found, so nothing depends on a cleanup job to be correct.
func (s *Store) TeamInvitationByToken(ctx context.Context, tokenHash string) (*TeamInvitationView, error) {
	var view TeamInvitationView
	err := s.pool.QueryRow(ctx, `
		select i.id, i.team_id, i.email, i.role, i.transfer, i.invited_by,
		       i.expires_at, i.accepted_at, i.created_at, t.name, coalesce(u.name, '')
		from team_invitations i
		join teams t on t.id = i.team_id
		left join users u on u.id = i.invited_by
		where i.token_hash = $1 and i.accepted_at is null and i.expires_at > now()`,
		tokenHash,
	).Scan(&view.ID, &view.TeamID, &view.Email, &view.Role, &view.Transfer,
		&view.InvitedBy, &view.ExpiresAt, &view.AcceptedAt, &view.CreatedAt,
		&view.TeamName, &view.InvitedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("teams: invitation by token: %w", err)
	}
	return &view, nil
}

// AcceptTeamInvitation spends one link for the account it was sent to. A regular
// invitation makes a manager or member; an owner invitation is a transfer, so
// the current owner steps down to manager as the new one steps up.
func (s *Store) AcceptTeamInvitation(ctx context.Context, tokenHash string, userID int64) (*Team, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("teams: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var invitation TeamInvitation
	var accountEmail string
	err = tx.QueryRow(ctx, `
		select i.id, i.team_id, i.email, i.role, i.transfer, i.invited_by,
		       i.expires_at, i.accepted_at, i.created_at, u.email
		from team_invitations i
		join users u on u.id = $2
		where i.token_hash = $1 and i.accepted_at is null and i.expires_at > now()
		for update of i`, tokenHash, userID,
	).Scan(&invitation.ID, &invitation.TeamID, &invitation.Email, &invitation.Role,
		&invitation.Transfer, &invitation.InvitedBy, &invitation.ExpiresAt,
		&invitation.AcceptedAt, &invitation.CreatedAt, &accountEmail)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("teams: accept invitation: %w", err)
	}
	if !strings.EqualFold(accountEmail, invitation.Email) {
		return nil, ErrTeamInvitationMail
	}

	if _, err := tx.Exec(ctx, `
		update team_invitations set accepted_at = now() where id = $1`,
		invitation.ID); err != nil {
		return nil, fmt.Errorf("teams: spend invitation: %w", err)
	}

	if invitation.Role == TeamRoleOwner {
		if _, err := tx.Exec(ctx, `
			update team_members set role = 'manager'
			where team_id = $1 and role = 'owner' and user_id <> $2`,
			invitation.TeamID, userID); err != nil {
			return nil, fmt.Errorf("teams: step the old owner down: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			insert into team_members (team_id, user_id, role) values ($1, $2, 'owner')
			on conflict (team_id, user_id) do update set role = 'owner'`,
			invitation.TeamID, userID); err != nil {
			return nil, fmt.Errorf("teams: make the new owner: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			insert into team_members (team_id, user_id, role) values ($1, $2, $3)
			on conflict (team_id, user_id) do update set role = excluded.role
			where team_members.role <> 'owner'`,
			invitation.TeamID, userID, invitation.Role); err != nil {
			return nil, fmt.Errorf("teams: add the invitee: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("teams: commit: %w", err)
	}
	return s.GetTeam(ctx, invitation.TeamID, userID)
}

// RevokeTeamInvitation removes one unaccepted link. An accepted invitation is
// history and is left alone.
func (s *Store) RevokeTeamInvitation(ctx context.Context, teamID, invitationID int64) error {
	tag, err := s.pool.Exec(ctx, `
		delete from team_invitations
		where id = $1 and team_id = $2 and accepted_at is null`, invitationID, teamID)
	if err != nil {
		return fmt.Errorf("teams: revoke invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
