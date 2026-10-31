package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// Organizations is what the API needs of the organizations' service.
type Organizations interface {
	Permissions(ctx context.Context, v orgs.Viewer) ([]string, error)
	Create(ctx context.Context, v orgs.Viewer, slug, name string) (orgs.Organization, error)
	Organizations(ctx context.Context, v orgs.Viewer) ([]orgs.Organization, error)
	Get(ctx context.Context, v orgs.Viewer, slug string) (orgs.Organization, error)
	ViewerPermissions(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) ([]string, error)
	Update(ctx context.Context, v orgs.Viewer, orgID uuid.UUID, settings orgs.Settings) (orgs.Organization, error)
	Roles(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) ([]orgs.Role, error)
	BuiltInRoles(ctx context.Context) ([]orgs.Role, error)
	CreateRole(ctx context.Context, v orgs.Viewer, orgID uuid.UUID, in orgs.RoleInput) (orgs.Role, error)
	UpdateRole(ctx context.Context, v orgs.Viewer, orgID, roleID uuid.UUID, in orgs.RoleInput) (orgs.Role, error)
	DeleteRole(ctx context.Context, v orgs.Viewer, orgID, roleID uuid.UUID) error
	GrantRole(ctx context.Context, v orgs.Viewer, orgID, userID, roleID uuid.UUID) error
	RevokeRole(ctx context.Context, v orgs.Viewer, orgID, userID, roleID uuid.UUID) error
	Members(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) ([]orgs.Member, error)
	Invite(ctx context.Context, v orgs.Viewer, orgID uuid.UUID, email string, roleID uuid.UUID) (orgs.Invitation, error)
	Invitations(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) ([]orgs.Invitation, error)
	CancelInvitation(ctx context.Context, v orgs.Viewer, orgID, id uuid.UUID) error
	MyInvitations(ctx context.Context, v orgs.Viewer) ([]orgs.Invitation, error)
	AcceptInvitation(ctx context.Context, v orgs.Viewer, id uuid.UUID) (orgs.Organization, error)
	DeclineInvitation(ctx context.Context, v orgs.Viewer, id uuid.UUID) error
	RequestToJoin(ctx context.Context, v orgs.Viewer, slug, note string) (orgs.JoinRequest, error)
	MyJoinRequests(ctx context.Context, v orgs.Viewer) ([]orgs.JoinRequest, error)
	JoinRequests(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) ([]orgs.JoinRequest, error)
	ApproveJoinRequest(ctx context.Context, v orgs.Viewer, orgID, id, roleID uuid.UUID) error
	DeclineJoinRequest(ctx context.Context, v orgs.Viewer, orgID, id uuid.UUID) error
	Leave(ctx context.Context, v orgs.Viewer, orgID uuid.UUID) error
	RemoveMember(ctx context.Context, v orgs.Viewer, orgID, userID uuid.UUID) error
}

// apiError is an operation's refusal: the problem the response error handler writes, with its status, stable code
// and detail. An operation returns one instead of a typed response, so every refusal takes the same path.
type apiError struct {
	status       int
	code, detail string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.status, e.code, e.detail)
}

// refuse is an apiError.
func refuse(status int, code, detail string) error {
	return &apiError{status: status, code: code, detail: detail}
}

// errUnauthenticated refuses an operation that needs a signed-in viewer and has Anonymous.
var errUnauthenticated = refuse(http.StatusUnauthorized, "auth.unauthenticated", "Sign in first.")

// viewer returns who the request signs in, or errUnauthenticated.
func viewer(ctx context.Context) (orgs.Viewer, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return orgs.Viewer{}, errUnauthenticated
	}
	return v.User, nil
}

// conflicts maps the domain's conflicts to their codes.
var conflicts = map[error]string{
	orgs.ErrSlugTaken:     "slug.taken",
	orgs.ErrInvited:       "invitation.exists",
	orgs.ErrRequested:     "join_request.exists",
	orgs.ErrAlreadyMember: "member.exists",
	orgs.ErrLastOwner:     "org.last_owner",
	orgs.ErrInviterGone:   "invitation.inviter_gone",
	orgs.ErrRoleNameTaken: "role.name_taken",
	orgs.ErrRoleInUse:     "role.in_use",
	orgs.ErrBuiltInRole:   "role.built_in",
	orgs.ErrGranted:       "grant.exists",
}

// fail maps the domain's errors to problems: kind names what wasn't found. Any other error stays one, which the
// response error handler logs and answers as internal.
func fail(err error, kind string) error {
	var verr *orgs.ValidationError
	switch {
	case errors.As(err, &verr):
		return refuse(http.StatusBadRequest, verr.Code, verr.Message)
	case errors.Is(err, auth.ErrForbidden):
		return refuse(http.StatusForbidden, "auth.forbidden", "You lack the permission this takes.")
	case errors.Is(err, orgs.ErrNotFound):
		return refuse(http.StatusNotFound, kind+".not_found", "There is no such "+kind+", or you may not see it.")
	case errors.Is(err, orgs.ErrNotMember):
		return refuse(http.StatusNotFound, "member.not_found", "You don't belong to this organization.")
	}
	for domain, code := range conflicts {
		if errors.Is(err, domain) {
			return refuse(http.StatusConflict, code, domain.Error())
		}
	}
	return err
}

// organization is o as the API shows it, with what the viewer holds there.
func organization(o orgs.Organization, held []string) Organization {
	out := Organization{Id: o.ID, Slug: o.Slug, Name: o.Name, MembershipRoleId: o.MembershipRoleID,
		AcceptsJoinRequests: o.AcceptsJoinRequests, CreatedAt: o.CreatedAt, ViewerPermissions: nonNil(held)}
	if o.Providers != nil {
		out.Providers = &o.Providers
	}
	return out
}

// nonNil is s, or an empty slice for nil, so the API shows [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func role(r orgs.Role) Role {
	out := Role{Id: r.ID, Name: r.Name, Description: r.Description, Permissions: nonNil(r.Permissions), BuiltIn: r.BuiltIn}
	if r.OrgID != uuid.Nil {
		out.OrgId = &r.OrgID
	}
	return out
}

func invitation(i orgs.Invitation) Invitation {
	return Invitation{Id: i.ID, OrgId: i.OrgID, OrgSlug: i.OrgSlug, OrgName: i.OrgName, Email: i.Email, RoleId: i.RoleID, RoleName: i.RoleName,
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

func joinRequest(r orgs.JoinRequest) JoinRequest {
	return JoinRequest{Id: r.ID, OrgId: r.OrgID, OrgSlug: r.OrgSlug, OrgName: r.OrgName, UserId: r.UserID, UserName: r.UserName,
		Note: r.Note, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
}

// mapAll applies f to each of in.
func mapAll[In, Out any](in []In, f func(In) Out) []Out {
	out := make([]Out, 0, len(in))
	for _, each := range in {
		out = append(out, f(each))
	}
	return out
}

// withPermissions is org as the API shows it, with the viewer's permissions read.
func (a api) withPermissions(ctx context.Context, v orgs.Viewer, org orgs.Organization) (Organization, error) {
	held, err := a.orgs.ViewerPermissions(ctx, v, org.ID)
	if err != nil {
		return Organization{}, fail(err, "organization")
	}
	return organization(org, held), nil
}

// org finds the organization the path names, for a viewer with org.read.
func (a api) org(ctx context.Context, v orgs.Viewer, slug string) (orgs.Organization, error) {
	org, err := a.orgs.Get(ctx, v, slug)
	if err != nil {
		return orgs.Organization{}, fail(err, "organization")
	}
	return org, nil
}

// ListMyOrganizations lists the viewer's organizations.
func (a api) ListMyOrganizations(ctx context.Context, _ ListMyOrganizationsRequestObject) (ListMyOrganizationsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	list, err := a.orgs.Organizations(ctx, v)
	if err != nil {
		return nil, fail(err, "organization")
	}
	out := make([]Organization, 0, len(list))
	for _, o := range list {
		shown, err := a.withPermissions(ctx, v, o)
		if err != nil {
			return nil, err
		}
		out = append(out, shown)
	}
	return ListMyOrganizations200JSONResponse{Organizations: out}, nil
}

// CreateOrganization makes an organization with the viewer its first Owner.
func (a api) CreateOrganization(ctx context.Context, req CreateOrganizationRequestObject) (CreateOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.orgs.Create(ctx, v, req.Body.Slug, req.Body.Name)
	if err != nil {
		return nil, fail(err, "organization")
	}
	shown, err := a.withPermissions(ctx, v, org)
	if err != nil {
		return nil, err
	}
	return CreateOrganization201JSONResponse(shown), nil
}

// GetOrganization answers the organization the slug names.
func (a api) GetOrganization(ctx context.Context, req GetOrganizationRequestObject) (GetOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	shown, err := a.withPermissions(ctx, v, org)
	if err != nil {
		return nil, err
	}
	return GetOrganization200JSONResponse(shown), nil
}

// UpdateOrganization changes what the body names and keeps the rest.
func (a api) UpdateOrganization(ctx context.Context, req UpdateOrganizationRequestObject) (UpdateOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	settings := orgs.Settings{Slug: org.Slug, Name: org.Name, MembershipRoleID: org.MembershipRoleID, AcceptsJoinRequests: org.AcceptsJoinRequests, Providers: org.Providers}
	b := req.Body
	if b.Slug != nil {
		settings.Slug = *b.Slug
	}
	if b.Name != nil {
		settings.Name = *b.Name
	}
	if b.MembershipRoleId != nil {
		settings.MembershipRoleID = *b.MembershipRoleId
	}
	if b.AcceptsJoinRequests != nil {
		settings.AcceptsJoinRequests = *b.AcceptsJoinRequests
	}
	if b.Providers != nil {
		settings.Providers = *b.Providers
		if len(settings.Providers) == 0 {
			settings.Providers = nil
		}
	}
	updated, err := a.orgs.Update(ctx, v, org.ID, settings)
	if err != nil {
		return nil, fail(err, "organization")
	}
	shown, err := a.withPermissions(ctx, v, updated)
	if err != nil {
		return nil, err
	}
	return UpdateOrganization200JSONResponse(shown), nil
}

// Deprecated routes announce their end with these headers (RFC 9745 and RFC 8594): /roles went when roles became
// an organization's, and goes two minor releases later.
const (
	rolesDeprecation = "@1793404800" // 2026-10-31
	rolesSunset      = "Sun, 31 Jan 2027 00:00:00 GMT"
)

// ListBuiltInRoles lists the built-in organization roles: deprecated, the organization's roles list them all.
func (a api) ListBuiltInRoles(ctx context.Context, req ListBuiltInRolesRequestObject) (ListBuiltInRolesResponseObject, error) {
	if _, err := viewer(ctx); err != nil {
		return nil, err
	}
	if !req.Params.Scope.Valid() {
		return nil, refuse(http.StatusBadRequest, "request.invalid", "Roles are listed at scope organization.")
	}
	roles, err := a.orgs.BuiltInRoles(ctx)
	if err != nil {
		return nil, fail(err, "role")
	}
	deprecation, sunset := rolesDeprecation, rolesSunset
	out := ListBuiltInRoles200JSONResponse{Headers: ListBuiltInRoles200ResponseHeaders{Deprecation: &deprecation, Sunset: &sunset}}
	out.Body.Roles = mapAll(roles, role)
	return out, nil
}

// ListPermissions lists the permissions a role at a scope may hold.
func (a api) ListPermissions(ctx context.Context, req ListPermissionsRequestObject) (ListPermissionsResponseObject, error) {
	if _, err := viewer(ctx); err != nil {
		return nil, err
	}
	if !req.Params.Scope.Valid() {
		return nil, refuse(http.StatusBadRequest, "request.invalid", "Permissions are listed at scope organization.")
	}
	return ListPermissions200JSONResponse{Permissions: orgs.RolePermissions()}, nil
}

// ListRoles lists the roles the organization grants.
func (a api) ListRoles(ctx context.Context, req ListRolesRequestObject) (ListRolesResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	roles, err := a.orgs.Roles(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListRoles200JSONResponse{Roles: mapAll(roles, role)}, nil
}

// CreateRole defines a custom role.
func (a api) CreateRole(ctx context.Context, req CreateRoleRequestObject) (CreateRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	in := orgs.RoleInput{Name: req.Body.Name, Permissions: req.Body.Permissions}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}
	r, err := a.orgs.CreateRole(ctx, v, org.ID, in)
	if err != nil {
		return nil, fail(err, "role")
	}
	return CreateRole201JSONResponse(role(r)), nil
}

// UpdateRole changes what the body names about a custom role and keeps the rest.
func (a api) UpdateRole(ctx context.Context, req UpdateRoleRequestObject) (UpdateRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	roles, err := a.orgs.Roles(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	i := slices.IndexFunc(roles, func(r orgs.Role) bool { return r.ID == req.RoleID })
	if i < 0 {
		return nil, fail(orgs.ErrNotFound, "role")
	}
	in := orgs.RoleInput{Name: roles[i].Name, Description: roles[i].Description, Permissions: roles[i].Permissions}
	if req.Body.Name != nil {
		in.Name = *req.Body.Name
	}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}
	if req.Body.Permissions != nil {
		in.Permissions = *req.Body.Permissions
	}
	r, err := a.orgs.UpdateRole(ctx, v, org.ID, req.RoleID, in)
	if err != nil {
		return nil, fail(err, "role")
	}
	return UpdateRole200JSONResponse(role(r)), nil
}

// DeleteRole deletes a custom role nobody holds.
func (a api) DeleteRole(ctx context.Context, req DeleteRoleRequestObject) (DeleteRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.DeleteRole(ctx, v, org.ID, req.RoleID); err != nil {
		return nil, fail(err, "role")
	}
	return DeleteRole204Response{}, nil
}

// GrantRole gives a member a role.
func (a api) GrantRole(ctx context.Context, req GrantRoleRequestObject) (GrantRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.GrantRole(ctx, v, org.ID, req.UserID, req.Body.RoleId); err != nil {
		return nil, fail(err, "member")
	}
	return GrantRole204Response{}, nil
}

// RevokeRole takes a role from a member.
func (a api) RevokeRole(ctx context.Context, req RevokeRoleRequestObject) (RevokeRoleResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.RevokeRole(ctx, v, org.ID, req.UserID, req.RoleID); err != nil {
		return nil, fail(err, "grant")
	}
	return RevokeRole204Response{}, nil
}

// ListMembers lists the organization's members.
func (a api) ListMembers(ctx context.Context, req ListMembersRequestObject) (ListMembersResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	members, err := a.orgs.Members(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListMembers200JSONResponse{Members: mapAll(members, func(m orgs.Member) Member {
		return Member{UserId: m.UserID, DisplayName: m.DisplayName, JoinedAt: m.JoinedAt, Roles: mapAll(m.Roles, role)}
	})}, nil
}

// RemoveMember drops a member.
func (a api) RemoveMember(ctx context.Context, req RemoveMemberRequestObject) (RemoveMemberResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.RemoveMember(ctx, v, org.ID, req.UserID); err != nil {
		return nil, fail(err, "member")
	}
	return RemoveMember204Response{}, nil
}

// LeaveOrganization drops the viewer from the organization.
func (a api) LeaveOrganization(ctx context.Context, req LeaveOrganizationRequestObject) (LeaveOrganizationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.Leave(ctx, v, org.ID); err != nil {
		return nil, fail(err, "organization")
	}
	return LeaveOrganization204Response{}, nil
}

// ListInvitations lists the organization's invitations that wait.
func (a api) ListInvitations(ctx context.Context, req ListInvitationsRequestObject) (ListInvitationsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.orgs.Invitations(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListInvitations200JSONResponse{Invitations: mapAll(list, invitation)}, nil
}

// Invite invites an email to the organization.
func (a api) Invite(ctx context.Context, req InviteRequestObject) (InviteResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	inv, err := a.orgs.Invite(ctx, v, org.ID, req.Body.Email, deref(req.Body.RoleId))
	if err != nil {
		return nil, fail(err, "organization")
	}
	return Invite201JSONResponse(invitation(inv)), nil
}

// deref is *id, or uuid.Nil for nil.
func deref(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// CancelInvitation ends an invitation that waits.
func (a api) CancelInvitation(ctx context.Context, req CancelInvitationRequestObject) (CancelInvitationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.CancelInvitation(ctx, v, org.ID, req.Id); err != nil {
		return nil, fail(err, "invitation")
	}
	return CancelInvitation204Response{}, nil
}

// ListMyInvitations lists the invitations waiting for the viewer.
func (a api) ListMyInvitations(ctx context.Context, _ ListMyInvitationsRequestObject) (ListMyInvitationsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	list, err := a.orgs.MyInvitations(ctx, v)
	if err != nil {
		return nil, fail(err, "invitation")
	}
	return ListMyInvitations200JSONResponse{Invitations: mapAll(list, invitation)}, nil
}

// AcceptInvitation joins an organization through an invitation.
func (a api) AcceptInvitation(ctx context.Context, req AcceptInvitationRequestObject) (AcceptInvitationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.orgs.AcceptInvitation(ctx, v, req.Id)
	if err != nil {
		return nil, fail(err, "invitation")
	}
	shown, err := a.withPermissions(ctx, v, org)
	if err != nil {
		return nil, err
	}
	return AcceptInvitation200JSONResponse(shown), nil
}

// DeclineInvitation declines an invitation.
func (a api) DeclineInvitation(ctx context.Context, req DeclineInvitationRequestObject) (DeclineInvitationResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.DeclineInvitation(ctx, v, req.Id); err != nil {
		return nil, fail(err, "invitation")
	}
	return DeclineInvitation204Response{}, nil
}

// ListMyJoinRequests lists the viewer's requests that wait.
func (a api) ListMyJoinRequests(ctx context.Context, _ ListMyJoinRequestsRequestObject) (ListMyJoinRequestsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	list, err := a.orgs.MyJoinRequests(ctx, v)
	if err != nil {
		return nil, fail(err, "join_request")
	}
	return ListMyJoinRequests200JSONResponse{JoinRequests: mapAll(list, joinRequest)}, nil
}

// RequestToJoin asks to join the organization the slug names.
func (a api) RequestToJoin(ctx context.Context, req RequestToJoinRequestObject) (RequestToJoinResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	note := ""
	if req.Body != nil && req.Body.Note != nil {
		note = *req.Body.Note
	}
	r, err := a.orgs.RequestToJoin(ctx, v, req.Org, note)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return RequestToJoin201JSONResponse(joinRequest(r)), nil
}

// ListJoinRequests lists the requests waiting at the organization.
func (a api) ListJoinRequests(ctx context.Context, req ListJoinRequestsRequestObject) (ListJoinRequestsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	list, err := a.orgs.JoinRequests(ctx, v, org.ID)
	if err != nil {
		return nil, fail(err, "organization")
	}
	return ListJoinRequests200JSONResponse{JoinRequests: mapAll(list, joinRequest)}, nil
}

// ApproveJoinRequest makes the requester a member.
func (a api) ApproveJoinRequest(ctx context.Context, req ApproveJoinRequestRequestObject) (ApproveJoinRequestResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	var roleID uuid.UUID
	if req.Body != nil {
		roleID = deref(req.Body.RoleId)
	}
	if err := a.orgs.ApproveJoinRequest(ctx, v, org.ID, req.Id, roleID); err != nil {
		return nil, fail(err, "join_request")
	}
	return ApproveJoinRequest204Response{}, nil
}

// DeclineJoinRequest declines a request that waits.
func (a api) DeclineJoinRequest(ctx context.Context, req DeclineJoinRequestRequestObject) (DeclineJoinRequestResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	org, err := a.org(ctx, v, req.Org)
	if err != nil {
		return nil, err
	}
	if err := a.orgs.DeclineJoinRequest(ctx, v, org.ID, req.Id); err != nil {
		return nil, fail(err, "join_request")
	}
	return DeclineJoinRequest204Response{}, nil
}
