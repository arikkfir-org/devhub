package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// OrgStore keeps organizations' rows (db/migrations/00004_organizations.sql): orgs.Store on PostgreSQL, and the
// grants the authorizer reads (auth.Grants), through the queries sqlc generates from db/queries.
type OrgStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewOrgStore returns organizations' store on pool's database.
func NewOrgStore(pool *pgxpool.Pool) *OrgStore {
	return &OrgStore{pool: pool, q: queries.New(pool)}
}

var (
	_ orgs.Store  = (*OrgStore)(nil)
	_ auth.Grants = (*OrgStore)(nil)
)

// conflicts maps the constraints a unique violation names to the domain's errors.
var conflicts = map[string]error{
	"organization_slugs_pkey": orgs.ErrSlugTaken,
	"organizations_slug_key":  orgs.ErrSlugTaken,
	"invitations_pending":     orgs.ErrInvited,
	"join_requests_pending":   orgs.ErrRequested,
	"memberships_pkey":        orgs.ErrAlreadyMember,
	"roles_name":              orgs.ErrRoleNameTaken,
}

// transact runs fn in a transaction that acts for actor (transact), mapping a conflict to the domain's error.
func (s *OrgStore) transact(ctx context.Context, what string, actor uuid.UUID, fn func(ctx context.Context, q *queries.Queries) error) error {
	err := transact(ctx, s.pool, s.q, what, actor, fn)
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		if domain, ok := conflicts[conflict.Constraint]; ok {
			return domain
		}
	}
	return err
}

// wrapOrg returns orgs.ErrNotFound for a query that found no row, and err in what's context otherwise.
func wrapOrg(what string, err error) error {
	if errors.Is(wrap(what, err), auth.ErrNotFound) {
		return orgs.ErrNotFound
	}
	return wrap(what, err)
}

// organization is the domain's view of an organizations row.
func organization(o queries.Organization) orgs.Organization {
	return orgs.Organization{ID: o.ID, Slug: o.Slug, Name: o.Name, MembershipRoleID: o.MembershipRoleID,
		AcceptsJoinRequests: o.AcceptsJoinRequests, Providers: o.Providers, CreatedAt: o.CreatedAt}
}

// Permissions lists what principalID's roles hold at the scope, through every grant it has there.
func (s *OrgStore) Permissions(ctx context.Context, principalID uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	var id *uuid.UUID
	if scopeID != uuid.Nil {
		id = &scopeID
	}
	permissions, err := s.q.Permissions(ctx, principalID, string(scope), id)
	if err != nil {
		return nil, fmt.Errorf("reading the permissions: %w", err)
	}
	return permissions, nil
}

// Create makes the organization, claims its slug, makes its creator a member and its first Owner, and records both.
func (s *OrgStore) Create(ctx context.Context, actor uuid.UUID, org orgs.Organization) error {
	return s.transact(ctx, "creating the organization", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertOrganization(ctx, queries.InsertOrganizationParams{
			ID: org.ID, Slug: org.Slug, Name: org.Name, MembershipRoleID: org.MembershipRoleID,
			AcceptsJoinRequests: org.AcceptsJoinRequests, Providers: org.Providers, CreatedAt: org.CreatedAt,
		}); err != nil {
			return fmt.Errorf("inserting the organization: %w", err)
		}
		if err := q.InsertSlug(ctx, org.Slug, org.ID); err != nil {
			return fmt.Errorf("claiming the slug: %w", err)
		}
		if err := orgEvent(ctx, q, &org.ID, actor, "org.created", org.ID, map[string]any{"slug": org.Slug, "name": org.Name}); err != nil {
			return err
		}
		return join(ctx, q, org.ID, actor, actor, auth.RoleOwner, org.CreatedAt, "created")
	})
}

// join makes userID a member of orgID holding roleID, and records it: how says what made them one. It holds the
// organization's lock, as every grant does, so a role being deleted can't be granted meanwhile.
func join(ctx context.Context, q *queries.Queries, orgID, actor, userID, roleID uuid.UUID, at time.Time, how string) error {
	if _, err := q.LockOrganization(ctx, orgID); err != nil {
		return wrapOrg("locking the organization", err)
	}
	if err := q.InsertMembership(ctx, orgID, userID, at); err != nil {
		return fmt.Errorf("inserting the membership: %w", err)
	}
	if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{
		PrincipalID: userID, RoleID: roleID, Scope: string(auth.ScopeOrganization), ScopeID: &orgID, Source: string(auth.SourceGrant),
	}); err != nil {
		return fmt.Errorf("inserting the grant: %w", err)
	}
	if err := dropPending(ctx, q, orgID, userID); err != nil {
		return err
	}
	return orgEvent(ctx, q, &orgID, actor, "member.joined", userID, map[string]any{"role": roleID, "how": how})
}

// dropPending deletes the pending invitations to the user's verified emails and their pending requests at the
// organization: moot once they are a member, and no way back in once they aren't.
func dropPending(ctx context.Context, q *queries.Queries, orgID, userID uuid.UUID) error {
	if _, err := q.DeleteUserInvitations(ctx, orgID, userID); err != nil {
		return fmt.Errorf("dropping the user's invitations: %w", err)
	}
	if _, err := q.DeleteUserJoinRequests(ctx, orgID, userID); err != nil {
		return fmt.Errorf("dropping the user's requests: %w", err)
	}
	return nil
}

// BySlug finds an organization by any slug it has had.
func (s *OrgStore) BySlug(ctx context.Context, slug string) (orgs.Organization, error) {
	row, err := s.q.OrganizationBySlug(ctx, slug)
	if err != nil {
		return orgs.Organization{}, wrapOrg("reading the organization", err)
	}
	return organization(row.Organization), nil
}

// ByID finds an organization.
func (s *OrgStore) ByID(ctx context.Context, id uuid.UUID) (orgs.Organization, error) {
	o, err := s.q.OrganizationByID(ctx, id)
	if err != nil {
		return orgs.Organization{}, wrapOrg("reading the organization", err)
	}
	return organization(o), nil
}

// Update writes the organization's settings, claiming its slug when it changed: one of its own former slugs comes back
// to it, another organization's is taken.
func (s *OrgStore) Update(ctx context.Context, actor uuid.UUID, org orgs.Organization) error {
	return s.transact(ctx, "updating the organization", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := q.OrganizationByID(ctx, org.ID)
		if err != nil {
			return wrapOrg("reading the organization", err)
		}
		if before.Slug != org.Slug {
			claimed, err := q.ClaimSlug(ctx, org.Slug, org.ID)
			if err != nil {
				return fmt.Errorf("claiming the slug: %w", err)
			}
			if claimed == 0 {
				return orgs.ErrSlugTaken
			}
		}
		if _, err := q.UpdateOrganization(ctx, queries.UpdateOrganizationParams{
			ID: org.ID, Slug: org.Slug, Name: org.Name, MembershipRoleID: org.MembershipRoleID,
			AcceptsJoinRequests: org.AcceptsJoinRequests, Providers: org.Providers,
		}); err != nil {
			return fmt.Errorf("updating the organization: %w", err)
		}
		return orgEvent(ctx, q, &org.ID, actor, "org.updated", org.ID, map[string]any{"slug": org.Slug, "name": org.Name})
	})
}

// OrganizationsOf lists the organizations a user belongs to, by name.
func (s *OrgStore) OrganizationsOf(ctx context.Context, userID uuid.UUID) ([]orgs.Organization, error) {
	rows, err := s.q.OrganizationsOf(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the organizations: %w", err)
	}
	out := make([]orgs.Organization, 0, len(rows))
	for _, r := range rows {
		out = append(out, organization(r.Organization))
	}
	return out, nil
}

// role is the domain's view of a roles row.
func role(r queries.Role) orgs.Role {
	out := orgs.Role{ID: r.ID, Name: r.Name, Description: r.Description, Permissions: r.Permissions, BuiltIn: r.BuiltIn}
	if r.OrgID != nil {
		out.OrgID = *r.OrgID
	}
	return out
}

// rolesHeld lists the roles each principal holds at the organization.
func (s *OrgStore) rolesHeld(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID][]orgs.Role, error) {
	rows, err := s.q.OrganizationGrants(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the organization's grants: %w", err)
	}
	held := map[uuid.UUID][]orgs.Role{}
	for _, r := range rows {
		held[r.PrincipalID] = append(held[r.PrincipalID], role(r.Role))
	}
	return held, nil
}

// Members lists the organization's members with the roles they hold there, by display name.
func (s *OrgStore) Members(ctx context.Context, orgID uuid.UUID) ([]orgs.Member, error) {
	rows, err := s.q.Members(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the members: %w", err)
	}
	held, err := s.rolesHeld(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]orgs.Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, orgs.Member{UserID: r.UserID, DisplayName: r.DisplayName, JoinedAt: r.JoinedAt, Roles: held[r.UserID]})
	}
	return out, nil
}

// Member finds one member with their roles.
func (s *OrgStore) Member(ctx context.Context, orgID, userID uuid.UUID) (orgs.Member, error) {
	members, err := s.Members(ctx, orgID)
	if err != nil {
		return orgs.Member{}, err
	}
	for _, m := range members {
		if m.UserID == userID {
			return m, nil
		}
	}
	return orgs.Member{}, orgs.ErrNotFound
}

// MemberByEmail finds the member who holds email, verified.
func (s *OrgStore) MemberByEmail(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, error) {
	id, err := s.q.MemberByEmail(ctx, orgID, email)
	if err != nil {
		return uuid.Nil, wrapOrg("finding the member", err)
	}
	return id, nil
}

// Leave drops the member and every role they hold at the organization; actor is who did it. The organization's row
// is locked first, so two Owners leaving at once can't both go: the last one gets ErrLastOwner.
func (s *OrgStore) Leave(ctx context.Context, actor uuid.UUID, orgID, userID uuid.UUID, _ time.Time) error {
	return s.transact(ctx, "dropping the member", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if err := notLastOwner(ctx, q, orgID, userID); err != nil {
			return err
		}
		deleted, err := q.DeleteMembership(ctx, orgID, userID)
		if err != nil {
			return fmt.Errorf("deleting the membership: %w", err)
		}
		if deleted == 0 {
			return orgs.ErrNotFound
		}
		if _, err := q.DeleteScopeGrants(ctx, userID, string(auth.ScopeOrganization), orgID); err != nil {
			return fmt.Errorf("deleting the grants: %w", err)
		}
		if err := dropPending(ctx, q, orgID, userID); err != nil {
			return err
		}
		kind := "member.removed"
		if actor == userID {
			kind = "member.left"
		}
		return orgEvent(ctx, q, &orgID, actor, kind, userID, nil)
	})
}

// notLastOwner refuses to drop userID when they are the one Owner the organization has.
func notLastOwner(ctx context.Context, q *queries.Queries, orgID, userID uuid.UUID) error {
	owner, err := q.HoldsRole(ctx, userID, auth.RoleOwner, orgID)
	if err != nil {
		return fmt.Errorf("reading the member's roles: %w", err)
	}
	if !owner {
		return nil
	}
	owners, err := q.CountRoleHolders(ctx, auth.RoleOwner, orgID)
	if err != nil {
		return fmt.Errorf("counting the Owners: %w", err)
	}
	if owners <= 1 {
		return orgs.ErrLastOwner
	}
	return nil
}

// invitation is the domain's view of an invitations row with its organization's slug and name and its role's name.
func invitation(i queries.Invitation, slug, name, roleName string) orgs.Invitation {
	return orgs.Invitation{ID: i.ID, OrgID: i.OrgID, OrgSlug: slug, OrgName: name, Email: i.Email, RoleID: i.RoleID, RoleName: roleName,
		InvitedBy: i.InvitedBy, State: orgs.State(i.State), CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

// Invite records the invitation. It holds the organization's lock and reads the role under it, as every grant does,
// so a role being deleted isn't offered meanwhile.
func (s *OrgStore) Invite(ctx context.Context, actor uuid.UUID, inv orgs.Invitation) error {
	return s.transact(ctx, "recording the invitation", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, inv.OrgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if _, err := q.Role(ctx, inv.RoleID); err != nil {
			return wrapOrg("reading the role", err)
		}
		if _, err := q.DeleteExpiredInvitationTo(ctx, inv.OrgID, inv.Email, inv.CreatedAt); err != nil {
			return fmt.Errorf("dropping an expired invitation: %w", err)
		}
		if err := q.InsertInvitation(ctx, queries.InsertInvitationParams{
			ID: inv.ID, OrgID: inv.OrgID, Email: inv.Email, RoleID: inv.RoleID, InvitedBy: inv.InvitedBy,
			CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("inserting the invitation: %w", err)
		}
		return orgEvent(ctx, q, &inv.OrgID, actor, "invitation.made", inv.ID, map[string]any{"email": inv.Email, "role": inv.RoleID})
	})
}

// Invitation finds an invitation.
func (s *OrgStore) Invitation(ctx context.Context, id uuid.UUID) (orgs.Invitation, error) {
	row, err := s.q.Invitation(ctx, id)
	if err != nil {
		return orgs.Invitation{}, wrapOrg("reading the invitation", err)
	}
	return invitation(row.Invitation, row.Slug, row.Name, row.RoleName), nil
}

// Invitations lists the organization's pending invitations at now, oldest first.
func (s *OrgStore) Invitations(ctx context.Context, orgID uuid.UUID, now time.Time) ([]orgs.Invitation, error) {
	rows, err := s.q.PendingInvitations(ctx, orgID, now)
	if err != nil {
		return nil, fmt.Errorf("listing the invitations: %w", err)
	}
	out := make([]orgs.Invitation, 0, len(rows))
	for _, r := range rows {
		out = append(out, invitation(r.Invitation, r.Slug, r.Name, r.RoleName))
	}
	return out, nil
}

// InvitationsFor lists the pending invitations to any of emails at now, oldest first.
func (s *OrgStore) InvitationsFor(ctx context.Context, emails []string, now time.Time) ([]orgs.Invitation, error) {
	rows, err := s.q.InvitationsFor(ctx, emails, now)
	if err != nil {
		return nil, fmt.Errorf("listing the invitations: %w", err)
	}
	out := make([]orgs.Invitation, 0, len(rows))
	for _, r := range rows {
		out = append(out, invitation(r.Invitation, r.Slug, r.Name, r.RoleName))
	}
	return out, nil
}

// closeInvitation ends a pending invitation as state, at `at` by actor.
func closeInvitation(ctx context.Context, q *queries.Queries, actor uuid.UUID, inv orgs.Invitation, state orgs.State, at time.Time) error {
	closed, err := q.CloseInvitation(ctx, queries.CloseInvitationParams{ID: inv.ID, State: string(state), DecidedAt: &at, DecidedBy: &actor})
	if err != nil {
		return fmt.Errorf("closing the invitation: %w", err)
	}
	if closed == 0 {
		return orgs.ErrNotFound
	}
	return orgEvent(ctx, q, &inv.OrgID, actor, "invitation."+string(state), inv.ID, map[string]any{"email": inv.Email})
}

// Accept makes the invitee a member with the invitation's role, closes the invitation, and records both. It takes
// the organization's lock before touching the invitation, the order DeleteRole uses, so the two can't deadlock.
func (s *OrgStore) Accept(ctx context.Context, actor uuid.UUID, inv orgs.Invitation, userID uuid.UUID, at time.Time) error {
	return s.transact(ctx, "accepting the invitation", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, inv.OrgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if err := closeInvitation(ctx, q, actor, inv, orgs.StateAccepted, at); err != nil {
			return err
		}
		return join(ctx, q, inv.OrgID, actor, userID, inv.RoleID, at, "invitation")
	})
}

// CloseInvitation ends a pending invitation as state.
func (s *OrgStore) CloseInvitation(ctx context.Context, actor uuid.UUID, id uuid.UUID, state orgs.State, at time.Time) error {
	inv, err := s.Invitation(ctx, id)
	if err != nil {
		return err
	}
	return s.transact(ctx, "closing the invitation", actor, func(ctx context.Context, q *queries.Queries) error {
		return closeInvitation(ctx, q, actor, inv, state, at)
	})
}

// joinRequest is the domain's view of a join_requests row with its organization's and user's names.
func joinRequest(j queries.JoinRequest, slug, name, userName string) orgs.JoinRequest {
	return orgs.JoinRequest{ID: j.ID, OrgID: j.OrgID, OrgSlug: slug, OrgName: name, UserID: j.UserID, UserName: userName,
		Note: j.Note, State: orgs.State(j.State), CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt}
}

// Request records the join request.
func (s *OrgStore) Request(ctx context.Context, actor uuid.UUID, req orgs.JoinRequest) error {
	return s.transact(ctx, "recording the request", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.DeleteExpiredJoinRequestOf(ctx, req.OrgID, req.UserID, req.CreatedAt); err != nil {
			return fmt.Errorf("dropping an expired request: %w", err)
		}
		if err := q.InsertJoinRequest(ctx, queries.InsertJoinRequestParams{
			ID: req.ID, OrgID: req.OrgID, UserID: req.UserID, Note: req.Note, CreatedAt: req.CreatedAt, ExpiresAt: req.ExpiresAt,
		}); err != nil {
			return fmt.Errorf("inserting the request: %w", err)
		}
		return orgEvent(ctx, q, &req.OrgID, actor, "join_request.made", req.ID, nil)
	})
}

// JoinRequest finds a join request.
func (s *OrgStore) JoinRequest(ctx context.Context, id uuid.UUID) (orgs.JoinRequest, error) {
	row, err := s.q.JoinRequest(ctx, id)
	if err != nil {
		return orgs.JoinRequest{}, wrapOrg("reading the request", err)
	}
	return joinRequest(row.JoinRequest, row.Slug, row.Name, row.DisplayName), nil
}

// JoinRequests lists the organization's pending requests at now, oldest first.
func (s *OrgStore) JoinRequests(ctx context.Context, orgID uuid.UUID, now time.Time) ([]orgs.JoinRequest, error) {
	rows, err := s.q.PendingJoinRequests(ctx, orgID, now)
	if err != nil {
		return nil, fmt.Errorf("listing the requests: %w", err)
	}
	out := make([]orgs.JoinRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, joinRequest(r.JoinRequest, r.Slug, r.Name, r.DisplayName))
	}
	return out, nil
}

// JoinRequestsOf lists the user's pending requests at now, oldest first.
func (s *OrgStore) JoinRequestsOf(ctx context.Context, userID uuid.UUID, now time.Time) ([]orgs.JoinRequest, error) {
	rows, err := s.q.JoinRequestsOf(ctx, userID, now)
	if err != nil {
		return nil, fmt.Errorf("listing the requests: %w", err)
	}
	out := make([]orgs.JoinRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, joinRequest(r.JoinRequest, r.Slug, r.Name, r.DisplayName))
	}
	return out, nil
}

// closeJoinRequest ends a pending request as state, at `at` by actor.
func closeJoinRequest(ctx context.Context, q *queries.Queries, actor uuid.UUID, req orgs.JoinRequest, state orgs.State, at time.Time) error {
	closed, err := q.CloseJoinRequest(ctx, queries.CloseJoinRequestParams{ID: req.ID, State: string(state), DecidedAt: &at, DecidedBy: &actor})
	if err != nil {
		return fmt.Errorf("closing the request: %w", err)
	}
	if closed == 0 {
		return orgs.ErrNotFound
	}
	return orgEvent(ctx, q, &req.OrgID, actor, "join_request."+string(state), req.ID, map[string]any{"user": req.UserID})
}

// Approve makes the requester a member with roleID, closes the request, and records both.
func (s *OrgStore) Approve(ctx context.Context, actor uuid.UUID, req orgs.JoinRequest, roleID uuid.UUID, at time.Time) error {
	return s.transact(ctx, "approving the request", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := closeJoinRequest(ctx, q, actor, req, orgs.StateApproved, at); err != nil {
			return err
		}
		return join(ctx, q, req.OrgID, actor, req.UserID, roleID, at, "request")
	})
}

// CloseJoinRequest ends a pending request as state.
func (s *OrgStore) CloseJoinRequest(ctx context.Context, actor uuid.UUID, id uuid.UUID, state orgs.State, at time.Time) error {
	req, err := s.JoinRequest(ctx, id)
	if err != nil {
		return err
	}
	return s.transact(ctx, "closing the request", actor, func(ctx context.Context, q *queries.Queries) error {
		return closeJoinRequest(ctx, q, actor, req, state, at)
	})
}

// Role finds a role.
func (s *OrgStore) Role(ctx context.Context, id uuid.UUID) (orgs.Role, error) {
	r, err := s.q.Role(ctx, id)
	if err != nil {
		return orgs.Role{}, wrapOrg("reading the role", err)
	}
	return role(r), nil
}

// Roles lists the roles granted at the organization: the built-in organization roles first, then its own; for
// uuid.Nil, the built-in ones alone.
func (s *OrgStore) Roles(ctx context.Context, orgID uuid.UUID) ([]orgs.Role, error) {
	rows, err := s.q.OrganizationRoles(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the roles: %w", err)
	}
	out := make([]orgs.Role, 0, len(rows))
	for _, r := range rows {
		out = append(out, role(r))
	}
	return out, nil
}

// CreateRole records a custom role of its organization.
func (s *OrgStore) CreateRole(ctx context.Context, actor uuid.UUID, r orgs.Role) error {
	return s.transact(ctx, "creating the role", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertRole(ctx, queries.InsertRoleParams{ID: r.ID, Name: r.Name, Description: r.Description, Permissions: r.Permissions, OrgID: &r.OrgID}); err != nil {
			return fmt.Errorf("inserting the role: %w", err)
		}
		return orgEvent(ctx, q, &r.OrgID, actor, "role.created", r.ID, map[string]any{"name": r.Name, "permissions": r.Permissions})
	})
}

// UpdateRole rewrites one of its organization's own roles; a built-in or another organization's is not found.
func (s *OrgStore) UpdateRole(ctx context.Context, actor uuid.UUID, r orgs.Role) error {
	return s.transact(ctx, "updating the role", actor, func(ctx context.Context, q *queries.Queries) error {
		updated, err := q.UpdateRole(ctx, queries.UpdateRoleParams{ID: r.ID, OrgID: r.OrgID, Name: r.Name, Description: r.Description, Permissions: r.Permissions})
		if err != nil {
			return fmt.Errorf("updating the role: %w", err)
		}
		if updated == 0 {
			return orgs.ErrNotFound
		}
		return orgEvent(ctx, q, &r.OrgID, actor, "role.updated", r.ID, map[string]any{"name": r.Name, "permissions": r.Permissions})
	})
}

// DeleteRole deletes one of the organization's own roles that nobody holds, cancelling the invitations that offered
// it. The organization's row is locked first, and every grant takes that lock too, so no grant of the role lands
// between the check and the deletion.
func (s *OrgStore) DeleteRole(ctx context.Context, actor uuid.UUID, orgID, roleID uuid.UUID, at time.Time) error {
	return s.transact(ctx, "deleting the role", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		inUse, err := q.RoleInUse(ctx, roleID)
		if err != nil {
			return fmt.Errorf("checking the role's grants: %w", err)
		}
		if inUse {
			return orgs.ErrRoleInUse
		}
		deleted, err := q.DeleteRole(ctx, roleID, orgID)
		if err != nil {
			return fmt.Errorf("deleting the role: %w", err)
		}
		if deleted == 0 {
			return orgs.ErrNotFound
		}
		cancelled, err := q.CancelInvitationsOfRole(ctx, queries.CancelInvitationsOfRoleParams{OrgID: orgID, RoleID: roleID, DecidedAt: &at, DecidedBy: &actor})
		if err != nil {
			return fmt.Errorf("cancelling the role's invitations: %w", err)
		}
		for _, inv := range cancelled {
			if err := orgEvent(ctx, q, &orgID, actor, "invitation.cancelled", inv.ID, map[string]any{"email": inv.Email, "role": roleID}); err != nil {
				return err
			}
		}
		return orgEvent(ctx, q, &orgID, actor, "role.deleted", roleID, map[string]any{"invitationsCancelled": len(cancelled)})
	})
}

// GrantRole gives the member the role at the organization. It holds the organization's lock, so the role can't be
// deleted meanwhile: one deleted first is not found.
func (s *OrgStore) GrantRole(ctx context.Context, actor uuid.UUID, orgID, userID, roleID uuid.UUID) error {
	return s.transact(ctx, "granting the role", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if _, err := q.Role(ctx, roleID); err != nil {
			return wrapOrg("reading the role", err)
		}
		granted, err := q.InsertGrant(ctx, queries.InsertGrantParams{
			PrincipalID: userID, RoleID: roleID, Scope: string(auth.ScopeOrganization), ScopeID: &orgID, Source: string(auth.SourceGrant),
		})
		if err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		if granted == 0 {
			return orgs.ErrGranted
		}
		return orgEvent(ctx, q, &orgID, actor, "role.granted", userID, map[string]any{"role": roleID})
	})
}

// RevokeRole takes the role from the member at the organization. The organization's row is locked first, so the Owner
// role can't be taken from two Owners at once and leave none.
func (s *OrgStore) RevokeRole(ctx context.Context, actor uuid.UUID, orgID, userID, roleID uuid.UUID) error {
	return s.transact(ctx, "revoking the role", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, orgID); err != nil {
			return wrapOrg("locking the organization", err)
		}
		if roleID == auth.RoleOwner {
			if err := notLastOwner(ctx, q, orgID, userID); err != nil {
				return err
			}
		}
		revoked, err := q.DeleteOrganizationGrant(ctx, userID, roleID, orgID)
		if err != nil {
			return fmt.Errorf("deleting the grant: %w", err)
		}
		if revoked == 0 {
			return orgs.ErrNotFound
		}
		return orgEvent(ctx, q, &orgID, actor, "role.revoked", userID, map[string]any{"role": roleID})
	})
}

// DeleteExpired deletes the invitations and join requests that expired before now.
func (s *OrgStore) DeleteExpired(ctx context.Context, now time.Time) (invitations, requests int64, err error) {
	if invitations, err = s.q.DeleteExpiredInvitations(ctx, now); err != nil {
		return 0, 0, fmt.Errorf("deleting expired invitations: %w", err)
	}
	if requests, err = s.q.DeleteExpiredJoinRequests(ctx, now); err != nil {
		return 0, 0, fmt.Errorf("deleting expired join requests: %w", err)
	}
	return invitations, requests, nil
}
