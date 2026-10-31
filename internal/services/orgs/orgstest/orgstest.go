// Package orgstest holds a fake of the organizations domain's port, for the tests of its service and of the adapters
// that serve it: a Store in memory that also answers the authorizer's reads, with the built-in organization roles.
package orgstest

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// The built-in organization roles, as migration 2 writes them.
var (
	Owner  = orgs.Role{ID: auth.RoleOwner, Name: "Owner", BuiltIn: true, Permissions: slices.Concat(auth.OrganizationPermissions, auth.RepositoryPermissions)}
	Member = orgs.Role{ID: auth.RoleMember, Name: "Member", BuiltIn: true, Permissions: []string{"org.read", "repos.create", "agents.create", "agents.assign"}}
	Guest  = orgs.Role{ID: auth.RoleGuest, Name: "Guest", BuiltIn: true, Permissions: []string{"org.read"}}
	// Manager is a custom role for tests: it manages members but holds no grants.
	Manager = orgs.Role{ID: uuid.MustParse("00000000-0000-7000-8000-000000000f01"), Name: "Manager", Permissions: []string{"org.read", "org.members.manage"}}
	// The installation's roles, which the authorizer reads too.
	installationAdmin = orgs.Role{ID: auth.RoleInstallationAdmin, Name: "Installation admin", BuiltIn: true, Permissions: auth.InstallationPermissions}
	installationUser  = orgs.Role{ID: auth.RoleInstallationUser, Name: "Installation user", BuiltIn: true, Permissions: []string{"orgs.create"}}
)

// Grant is a role a principal holds at a scope.
type Grant struct {
	Principal uuid.UUID
	Role      uuid.UUID
	Scope     auth.Scope
	ScopeID   uuid.UUID
}

// Store is an orgs.Store and auth.Grants in memory. Fail, when set, fails every write.
type Store struct {
	Orgs        map[uuid.UUID]orgs.Organization
	Slugs       map[string]uuid.UUID
	Memberships map[uuid.UUID][]uuid.UUID // org → users
	Names       map[uuid.UUID]string      // user → display name
	Emails      map[uuid.UUID][]string    // user → verified emails
	Grants      []Grant
	Invites     map[uuid.UUID]orgs.Invitation
	Reqs        map[uuid.UUID]orgs.JoinRequest
	// RoleList holds the organization roles, Roles' answer; installation holds the installation's.
	RoleList     []orgs.Role
	installation []orgs.Role
	Events       []string
	Fail         error
}

// NewStore returns an empty Store with the built-in roles and Manager.
func NewStore() *Store {
	return &Store{
		Orgs: map[uuid.UUID]orgs.Organization{}, Slugs: map[string]uuid.UUID{}, Memberships: map[uuid.UUID][]uuid.UUID{},
		Names: map[uuid.UUID]string{}, Emails: map[uuid.UUID][]string{}, Invites: map[uuid.UUID]orgs.Invitation{},
		Reqs: map[uuid.UUID]orgs.JoinRequest{}, RoleList: []orgs.Role{Owner, Member, Guest, Manager},
		installation: []orgs.Role{installationAdmin, installationUser},
	}
}

// AddUser registers a user's display name and verified emails.
func (s *Store) AddUser(id uuid.UUID, name string, emails ...string) {
	s.Names[id], s.Emails[id] = name, emails
}

// Grant gives principal role at scope.
func (s *Store) Grant(principal, role uuid.UUID, scope auth.Scope, scopeID uuid.UUID) {
	g := Grant{Principal: principal, Role: role, Scope: scope, ScopeID: scopeID}
	if !slices.Contains(s.Grants, g) {
		s.Grants = append(s.Grants, g)
	}
}

// AddMember makes user a member of org holding role.
func (s *Store) AddMember(org, user, role uuid.UUID) {
	s.Memberships[org] = append(s.Memberships[org], user)
	s.Grant(user, role, auth.ScopeOrganization, org)
}

func (s *Store) role(id uuid.UUID) (orgs.Role, bool) {
	for _, r := range slices.Concat(s.RoleList, s.installation) {
		if r.ID == id {
			return r, true
		}
	}
	return orgs.Role{}, false
}

// Permissions lists what principal's roles hold at the scope.
func (s *Store) Permissions(_ context.Context, principal uuid.UUID, scope auth.Scope, scopeID uuid.UUID) ([]string, error) {
	var out []string
	for _, g := range s.Grants {
		if g.Principal == principal && g.Scope == scope && g.ScopeID == scopeID {
			if r, ok := s.role(g.Role); ok {
				out = append(out, r.Permissions...)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Create records the organization with its creator as Owner.
func (s *Store) Create(_ context.Context, actor uuid.UUID, org orgs.Organization) error {
	if s.Fail != nil {
		return s.Fail
	}
	if _, taken := s.Slugs[org.Slug]; taken {
		return orgs.ErrSlugTaken
	}
	s.Orgs[org.ID], s.Slugs[org.Slug] = org, org.ID
	s.AddMember(org.ID, actor, auth.RoleOwner)
	s.Events = append(s.Events, "org.created", "member.joined")
	return nil
}

// BySlug finds an organization by any of its slugs.
func (s *Store) BySlug(_ context.Context, slug string) (orgs.Organization, error) {
	id, ok := s.Slugs[slug]
	if !ok {
		return orgs.Organization{}, orgs.ErrNotFound
	}
	return s.Orgs[id], nil
}

// ByID finds an organization.
func (s *Store) ByID(_ context.Context, id uuid.UUID) (orgs.Organization, error) {
	org, ok := s.Orgs[id]
	if !ok {
		return orgs.Organization{}, orgs.ErrNotFound
	}
	return org, nil
}

// Update writes the organization, claiming a new slug.
func (s *Store) Update(_ context.Context, _ uuid.UUID, org orgs.Organization) error {
	if s.Fail != nil {
		return s.Fail
	}
	if owner, taken := s.Slugs[org.Slug]; taken && owner != org.ID {
		return orgs.ErrSlugTaken
	}
	s.Slugs[org.Slug] = org.ID
	s.Orgs[org.ID] = org
	s.Events = append(s.Events, "org.updated")
	return nil
}

// OrganizationsOf lists a user's organizations.
func (s *Store) OrganizationsOf(_ context.Context, user uuid.UUID) ([]orgs.Organization, error) {
	var out []orgs.Organization
	for id, users := range s.Memberships {
		if slices.Contains(users, user) {
			out = append(out, s.Orgs[id])
		}
	}
	slices.SortFunc(out, func(a, b orgs.Organization) int { return cmpStrings(a.Name, b.Name) })
	return out, nil
}

func cmpStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Members lists an organization's members with their roles.
func (s *Store) Members(ctx context.Context, org uuid.UUID) ([]orgs.Member, error) {
	var out []orgs.Member
	for _, user := range s.Memberships[org] {
		m, _ := s.Member(ctx, org, user)
		out = append(out, m)
	}
	return out, nil
}

// Member finds one member with their roles.
func (s *Store) Member(_ context.Context, org, user uuid.UUID) (orgs.Member, error) {
	if !slices.Contains(s.Memberships[org], user) {
		return orgs.Member{}, orgs.ErrNotFound
	}
	m := orgs.Member{UserID: user, DisplayName: s.Names[user]}
	for _, g := range s.Grants {
		if g.Principal == user && g.Scope == auth.ScopeOrganization && g.ScopeID == org {
			if r, ok := s.role(g.Role); ok {
				m.Roles = append(m.Roles, r)
			}
		}
	}
	return m, nil
}

// MemberByEmail finds the member holding email.
func (s *Store) MemberByEmail(_ context.Context, org uuid.UUID, email string) (uuid.UUID, error) {
	for _, user := range s.Memberships[org] {
		if slices.Contains(s.Emails[user], email) {
			return user, nil
		}
	}
	return uuid.Nil, orgs.ErrNotFound
}

// Leave drops the member and their roles at the organization, and what waited for them there; the last Owner can't go.
func (s *Store) Leave(_ context.Context, actor, org, user uuid.UUID, _ time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	if s.holds(user, auth.RoleOwner, org) && s.countRoleHolders(org, auth.RoleOwner) <= 1 {
		return orgs.ErrLastOwner
	}
	s.dropPending(org, user)
	s.Memberships[org] = slices.DeleteFunc(s.Memberships[org], func(u uuid.UUID) bool { return u == user })
	s.Grants = slices.DeleteFunc(s.Grants, func(g Grant) bool {
		return g.Principal == user && g.Scope == auth.ScopeOrganization && g.ScopeID == org
	})
	if actor == user {
		s.Events = append(s.Events, "member.left")
	} else {
		s.Events = append(s.Events, "member.removed")
	}
	return nil
}

// Invite records an invitation.
func (s *Store) Invite(_ context.Context, _ uuid.UUID, inv orgs.Invitation) error {
	if s.Fail != nil {
		return s.Fail
	}
	for id, other := range s.Invites {
		if other.OrgID == inv.OrgID && other.Email == inv.Email && other.State == orgs.StatePending {
			if other.ExpiresAt.After(inv.CreatedAt) {
				return orgs.ErrInvited
			}
			delete(s.Invites, id)
		}
	}
	s.Invites[inv.ID] = inv
	s.Events = append(s.Events, "invitation.made")
	return nil
}

// Invitation finds an invitation.
func (s *Store) Invitation(_ context.Context, id uuid.UUID) (orgs.Invitation, error) {
	inv, ok := s.Invites[id]
	if !ok {
		return orgs.Invitation{}, orgs.ErrNotFound
	}
	return inv, nil
}

// Invitations lists an organization's pending invitations.
func (s *Store) Invitations(_ context.Context, org uuid.UUID, now time.Time) ([]orgs.Invitation, error) {
	return s.pendingInvitations(func(i orgs.Invitation) bool { return i.OrgID == org }, now), nil
}

// InvitationsFor lists the pending invitations to the emails.
func (s *Store) InvitationsFor(_ context.Context, emails []string, now time.Time) ([]orgs.Invitation, error) {
	return s.pendingInvitations(func(i orgs.Invitation) bool { return slices.Contains(emails, i.Email) }, now), nil
}

func (s *Store) pendingInvitations(match func(orgs.Invitation) bool, now time.Time) []orgs.Invitation {
	var out []orgs.Invitation
	for _, i := range s.Invites {
		if match(i) && i.State == orgs.StatePending && now.Before(i.ExpiresAt) {
			out = append(out, i)
		}
	}
	slices.SortFunc(out, func(a, b orgs.Invitation) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Accept makes the invitee a member and closes the invitation.
func (s *Store) Accept(ctx context.Context, actor uuid.UUID, inv orgs.Invitation, user uuid.UUID, at time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	if err := s.CloseInvitation(ctx, actor, inv.ID, orgs.StateAccepted, at); err != nil {
		return err
	}
	s.AddMember(inv.OrgID, user, inv.RoleID)
	s.dropPending(inv.OrgID, user)
	s.Events = append(s.Events, "member.joined")
	return nil
}

// CloseInvitation ends a pending invitation.
func (s *Store) CloseInvitation(_ context.Context, _ uuid.UUID, id uuid.UUID, state orgs.State, _ time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	inv, ok := s.Invites[id]
	if !ok || inv.State != orgs.StatePending {
		return orgs.ErrNotFound
	}
	inv.State = state
	s.Invites[id] = inv
	s.Events = append(s.Events, "invitation."+string(state))
	return nil
}

// Request records a join request.
func (s *Store) Request(_ context.Context, _ uuid.UUID, req orgs.JoinRequest) error {
	if s.Fail != nil {
		return s.Fail
	}
	for id, other := range s.Reqs {
		if other.OrgID == req.OrgID && other.UserID == req.UserID && other.State == orgs.StatePending {
			if other.ExpiresAt.After(req.CreatedAt) {
				return orgs.ErrRequested
			}
			delete(s.Reqs, id)
		}
	}
	s.Reqs[req.ID] = req
	s.Events = append(s.Events, "join_request.made")
	return nil
}

// JoinRequest finds a request.
func (s *Store) JoinRequest(_ context.Context, id uuid.UUID) (orgs.JoinRequest, error) {
	req, ok := s.Reqs[id]
	if !ok {
		return orgs.JoinRequest{}, orgs.ErrNotFound
	}
	return req, nil
}

// JoinRequests lists an organization's pending requests.
func (s *Store) JoinRequests(_ context.Context, org uuid.UUID, now time.Time) ([]orgs.JoinRequest, error) {
	return s.pendingRequests(func(r orgs.JoinRequest) bool { return r.OrgID == org }, now), nil
}

// JoinRequestsOf lists a user's pending requests.
func (s *Store) JoinRequestsOf(_ context.Context, user uuid.UUID, now time.Time) ([]orgs.JoinRequest, error) {
	return s.pendingRequests(func(r orgs.JoinRequest) bool { return r.UserID == user }, now), nil
}

func (s *Store) pendingRequests(match func(orgs.JoinRequest) bool, now time.Time) []orgs.JoinRequest {
	var out []orgs.JoinRequest
	for _, r := range s.Reqs {
		if match(r) && r.State == orgs.StatePending && now.Before(r.ExpiresAt) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b orgs.JoinRequest) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// Approve makes the requester a member and closes the request.
func (s *Store) Approve(ctx context.Context, actor uuid.UUID, req orgs.JoinRequest, role uuid.UUID, at time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	if err := s.CloseJoinRequest(ctx, actor, req.ID, orgs.StateApproved, at); err != nil {
		return err
	}
	s.AddMember(req.OrgID, req.UserID, role)
	s.dropPending(req.OrgID, req.UserID)
	s.Events = append(s.Events, "member.joined")
	return nil
}

// CloseJoinRequest ends a pending request.
func (s *Store) CloseJoinRequest(_ context.Context, _ uuid.UUID, id uuid.UUID, state orgs.State, _ time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	req, ok := s.Reqs[id]
	if !ok || req.State != orgs.StatePending {
		return orgs.ErrNotFound
	}
	req.State = state
	s.Reqs[id] = req
	s.Events = append(s.Events, "join_request."+string(state))
	return nil
}

// Role finds a role.
func (s *Store) Role(_ context.Context, id uuid.UUID) (orgs.Role, error) {
	r, ok := s.role(id)
	if !ok {
		return orgs.Role{}, orgs.ErrNotFound
	}
	return r, nil
}

// Roles lists the roles granted at the organization: the built-in ones, Manager, and the organization's own.
func (s *Store) Roles(_ context.Context, org uuid.UUID) ([]orgs.Role, error) {
	var out []orgs.Role
	for _, r := range s.RoleList {
		if r.OrgID == uuid.Nil || r.OrgID == org {
			out = append(out, r)
		}
	}
	return out, nil
}

// CreateRole records a custom role.
func (s *Store) CreateRole(_ context.Context, _ uuid.UUID, role orgs.Role) error {
	if s.Fail != nil {
		return s.Fail
	}
	for _, r := range s.RoleList {
		if r.Name == role.Name && (r.OrgID == uuid.Nil || r.OrgID == role.OrgID) {
			return orgs.ErrRoleNameTaken
		}
	}
	s.RoleList = append(s.RoleList, role)
	s.Events = append(s.Events, "role.created")
	return nil
}

// UpdateRole rewrites one of the organization's own roles.
func (s *Store) UpdateRole(_ context.Context, _ uuid.UUID, role orgs.Role) error {
	if s.Fail != nil {
		return s.Fail
	}
	i := slices.IndexFunc(s.RoleList, func(r orgs.Role) bool { return r.ID == role.ID && r.OrgID == role.OrgID && !r.BuiltIn })
	if i < 0 {
		return orgs.ErrNotFound
	}
	s.RoleList[i] = role
	s.Events = append(s.Events, "role.updated")
	return nil
}

// DeleteRole deletes one of the organization's own roles that nobody holds, and cancels the invitations offering it.
func (s *Store) DeleteRole(_ context.Context, _ uuid.UUID, org, role uuid.UUID, _ time.Time) error {
	if s.Fail != nil {
		return s.Fail
	}
	i := slices.IndexFunc(s.RoleList, func(r orgs.Role) bool { return r.ID == role && (r.OrgID == org || r.OrgID == uuid.Nil) && !r.BuiltIn })
	if i < 0 {
		return orgs.ErrNotFound
	}
	if slices.ContainsFunc(s.Grants, func(g Grant) bool { return g.Role == role }) {
		return orgs.ErrRoleInUse
	}
	for _, o := range s.Orgs {
		if o.MembershipRoleID == role {
			return orgs.ErrRoleInUse
		}
	}
	for id, inv := range s.Invites {
		if inv.OrgID == org && inv.RoleID == role && inv.State == orgs.StatePending {
			inv.State = orgs.StateCancelled
			s.Invites[id] = inv
			s.Events = append(s.Events, "invitation.cancelled")
		}
	}
	s.RoleList = slices.Delete(s.RoleList, i, i+1)
	s.Events = append(s.Events, "role.deleted")
	return nil
}

// GrantRole gives the member the role at the organization.
func (s *Store) GrantRole(_ context.Context, _ uuid.UUID, org, user, role uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	if s.holds(user, role, org) {
		return orgs.ErrGranted
	}
	s.Grants = append(s.Grants, Grant{Principal: user, Role: role, Scope: auth.ScopeOrganization, ScopeID: org})
	s.Events = append(s.Events, "role.granted")
	return nil
}

// RevokeRole takes the role from the member at the organization; the Owner role stays with the last Owner.
func (s *Store) RevokeRole(_ context.Context, _ uuid.UUID, org, user, role uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	if role == auth.RoleOwner && s.holds(user, role, org) && s.countRoleHolders(org, role) <= 1 {
		return orgs.ErrLastOwner
	}
	g := Grant{Principal: user, Role: role, Scope: auth.ScopeOrganization, ScopeID: org}
	if !slices.Contains(s.Grants, g) {
		return orgs.ErrNotFound
	}
	s.Grants = slices.DeleteFunc(s.Grants, func(other Grant) bool { return other == g })
	s.Events = append(s.Events, "role.revoked")
	return nil
}

// countRoleHolders counts who holds role at the organization.
func (s *Store) countRoleHolders(org, role uuid.UUID) int {
	n := 0
	for _, g := range s.Grants {
		if g.Role == role && g.Scope == auth.ScopeOrganization && g.ScopeID == org {
			n++
		}
	}
	return n
}

// holds says whether the principal holds role at the organization.
func (s *Store) holds(principal, role, org uuid.UUID) bool {
	return slices.Contains(s.Grants, Grant{Principal: principal, Role: role, Scope: auth.ScopeOrganization, ScopeID: org})
}

// dropPending deletes the pending invitations to the user's emails and their pending requests at the organization.
func (s *Store) dropPending(org, user uuid.UUID) {
	for id, inv := range s.Invites {
		if inv.OrgID == org && inv.State == orgs.StatePending && slices.Contains(s.Emails[user], inv.Email) {
			delete(s.Invites, id)
		}
	}
	for id, req := range s.Reqs {
		if req.OrgID == org && req.State == orgs.StatePending && req.UserID == user {
			delete(s.Reqs, id)
		}
	}
}

// DeleteExpired drops what expired at `at`.
func (s *Store) DeleteExpired(_ context.Context, at time.Time) (int64, int64, error) {
	if s.Fail != nil {
		return 0, 0, s.Fail
	}
	var invitations, requests int64
	for id, i := range s.Invites {
		if i.State == orgs.StatePending && i.ExpiresAt.Before(at) {
			delete(s.Invites, id)
			invitations++
		}
	}
	for id, r := range s.Reqs {
		if r.State == orgs.StatePending && r.ExpiresAt.Before(at) {
			delete(s.Reqs, id)
			requests++
		}
	}
	return invitations, requests, nil
}
