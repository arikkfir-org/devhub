package graphql

import (
	"context"
	"errors"
	"log/slog"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

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

// The error codes a field's error carries in its extensions, beside unauthenticated.
const (
	forbidden    = "FORBIDDEN"
	notFound     = "NOT_FOUND"
	conflict     = "CONFLICT"
	badUserInput = "BAD_USER_INPUT"
	internal     = "INTERNAL"
)

// conflicts maps the domain's conflicts to their reasons.
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

// refuse is a field's error with a code and, when reason is set, a stable reason beside it.
func refuse(ctx context.Context, code, reason, message string) error {
	ext := map[string]any{"code": code}
	if reason != "" {
		ext["reason"] = reason
	}
	return &gqlerror.Error{Path: graphql.GetPath(ctx), Message: message, Extensions: ext}
}

// fail maps the domain's errors to a field's: kind names what wasn't found. Any other error is logged and answered as
// internal, with what failed and nothing of why.
func fail(ctx context.Context, err error, kind, what string) error {
	var verr *orgs.ValidationError
	switch {
	case errors.As(err, &verr):
		return refuse(ctx, badUserInput, verr.Code, verr.Message)
	case errors.Is(err, auth.ErrForbidden):
		return refuse(ctx, forbidden, "", "you lack the permission this takes")
	case errors.Is(err, orgs.ErrNotFound):
		return refuse(ctx, notFound, kind+".not_found", "there is no such "+kind+", or you may not see it")
	case errors.Is(err, orgs.ErrNotMember):
		return refuse(ctx, notFound, "member.not_found", "you don't belong to this organization")
	}
	for domain, reason := range conflicts {
		if errors.Is(err, domain) {
			return refuse(ctx, conflict, reason, domain.Error())
		}
	}
	slog.ErrorContext(ctx, what+" failed", "error", err)
	return refuse(ctx, internal, "", what+" failed")
}

// parseID is id as a UUID, or a NOT_FOUND error for anything else: no object has such an ID.
func parseID(ctx context.Context, id, kind string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, refuse(ctx, notFound, kind+".not_found", "there is no such "+kind)
	}
	return parsed, nil
}

// organization is o as the API shows it; its lazy fields resolve on their own.
func organization(o orgs.Organization) *Organization {
	return &Organization{ID: o.ID.String(), Slug: o.Slug, Name: o.Name, MembershipRoleID: o.MembershipRoleID.String(),
		AcceptsJoinRequests: o.AcceptsJoinRequests, Providers: o.Providers, CreatedAt: o.CreatedAt}
}

func role(r orgs.Role) *Role {
	out := &Role{ID: r.ID.String(), Name: r.Name, Description: r.Description, Permissions: nonNil(r.Permissions), BuiltIn: r.BuiltIn}
	if r.OrgID != uuid.Nil {
		id := r.OrgID.String()
		out.OrganizationID = &id
	}
	return out
}

// roleInput is input as the domain takes it.
func roleInput(input RoleInput) orgs.RoleInput {
	in := orgs.RoleInput{Name: input.Name, Permissions: input.Permissions}
	if input.Description != nil {
		in.Description = *input.Description
	}
	return in
}

// changeGrant grants or revokes a role of a member, through change, and answers true when it did.
func (r *mutationResolver) changeGrant(ctx context.Context, organizationID, userID, roleID string,
	change func(ctx context.Context, v orgs.Viewer, orgID, userID, roleID uuid.UUID) error, what string) (bool, error) {
	v, err := viewer(ctx)
	if err != nil {
		return false, err
	}
	role, err := parseID(ctx, roleID, "role")
	if err != nil {
		return false, err
	}
	return act(ctx, organizationID, userID, "member", func(orgID, id uuid.UUID) error {
		return change(ctx, v.User, orgID, id, role)
	}, what)
}

func member(m orgs.Member) *Member {
	return &Member{UserID: m.UserID.String(), DisplayName: m.DisplayName, JoinedAt: m.JoinedAt, Roles: mapAll(m.Roles, role)}
}

func invitation(i orgs.Invitation) *Invitation {
	return &Invitation{ID: i.ID.String(), OrganizationID: i.OrgID.String(), OrganizationSlug: i.OrgSlug, OrganizationName: i.OrgName,
		Email: i.Email, RoleID: i.RoleID.String(), RoleName: i.RoleName, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt}
}

func joinRequest(r orgs.JoinRequest) *JoinRequest {
	return &JoinRequest{ID: r.ID.String(), OrganizationID: r.OrgID.String(), OrganizationSlug: r.OrgSlug, OrganizationName: r.OrgName,
		UserID: r.UserID.String(), UserName: r.UserName, Note: r.Note, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
}

// nonNil is s, or an empty slice for nil, so the API shows [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// mapAll applies f to each of in.
func mapAll[In, Out any](in []In, f func(In) Out) []Out {
	out := make([]Out, 0, len(in))
	for _, each := range in {
		out = append(out, f(each))
	}
	return out
}

// settings is input as the domain takes it, or a NOT_FOUND error for a membership role no role has.
func settings(ctx context.Context, input OrganizationSettings) (orgs.Settings, error) {
	roleID, err := parseID(ctx, input.MembershipRoleID, "role")
	if err != nil {
		return orgs.Settings{}, err
	}
	return orgs.Settings{Slug: input.Slug, Name: input.Name, MembershipRoleID: roleID,
		AcceptsJoinRequests: input.AcceptsJoinRequests, Providers: input.Providers}, nil
}

// act runs an operation on an organization and one of its objects, both named by ID, and answers true when it did.
func act(ctx context.Context, orgID, id, kind string, op func(orgID, id uuid.UUID) error, what string) (bool, error) {
	org, err := parseID(ctx, orgID, "organization")
	if err != nil {
		return false, err
	}
	target, err := parseID(ctx, id, kind)
	if err != nil {
		return false, err
	}
	if err := op(org, target); err != nil {
		return false, fail(ctx, err, kind, what)
	}
	return true, nil
}

// resolveUser answers a viewer's field through read, for the signed-in viewer alone: User is only ever the viewer.
func resolveUser[T any](ctx context.Context, read func(context.Context, orgs.Viewer) (T, error), what string) (T, error) {
	var zero T
	v, err := viewer(ctx)
	if err != nil {
		return zero, err
	}
	out, err := read(ctx, v.User)
	if err != nil {
		return zero, fail(ctx, err, "organization", what)
	}
	return out, nil
}

// resolveOrg answers an organization's field through read, for the viewer.
func resolveOrg[T any](ctx context.Context, obj *Organization, read func(context.Context, orgs.Viewer, uuid.UUID) (T, error), what string) (T, error) {
	var zero T
	v, err := viewer(ctx)
	if err != nil {
		return zero, err
	}
	orgID, err := parseID(ctx, obj.ID, "organization")
	if err != nil {
		return zero, err
	}
	out, err := read(ctx, v.User, orgID)
	if err != nil {
		return zero, fail(ctx, err, "organization", what)
	}
	return out, nil
}
