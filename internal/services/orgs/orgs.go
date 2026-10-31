// Package orgs is the domain of organizations: creating one, its settings, its members, and the invitations and join
// requests that make members. It knows nothing of HTTP or PostgreSQL: adapters bring those through its port, Store,
// and every permission check goes through sign-in's authorizer
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "Organizations").
package orgs

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// Lifetimes, the principals design's defaults.
const (
	// InvitationLifetime is how long an invitation waits for its invitee.
	InvitationLifetime = 7 * 24 * time.Hour
	// JoinRequestLifetime is how long a join request waits for a member manager.
	JoinRequestLifetime = 7 * 24 * time.Hour
)

// Organization is an organization: a slug for its URLs, a name, and its settings.
type Organization struct {
	ID   uuid.UUID
	Slug string
	Name string
	// MembershipRoleID is the role everyone who joins gets: Member by default.
	MembershipRoleID uuid.UUID
	// AcceptsJoinRequests lets users ask to join by slug; off, the organization answers like one that doesn't exist.
	AcceptsJoinRequests bool
	// Providers lists the identity providers its members may sign in with, or nil for all of the installation's;
	// an empty list is nil too.
	Providers []string
	CreatedAt time.Time
}

// Settings is what a manager changes about an organization.
type Settings struct {
	Slug                string
	Name                string
	MembershipRoleID    uuid.UUID
	AcceptsJoinRequests bool
	Providers           []string
}

// Role is a role granted at an organization: one of the built-in ones, or a custom role the organization defined.
type Role struct {
	ID          uuid.UUID
	Name        string
	Description string
	Permissions []string
	BuiltIn     bool
	// OrgID is the organization a custom role belongs to; uuid.Nil for a built-in role, which every organization grants.
	OrgID uuid.UUID
}

// RoleInput is what defines a custom role: a name, a description, and the permissions it holds, from
// RolePermissions.
type RoleInput struct {
	Name        string
	Description string
	Permissions []string
}

// RolePermissions lists the permissions a role granted at an organization may hold: the catalog's organization and
// repository permissions, as the built-in Owner holds both.
func RolePermissions() []string {
	return slices.Concat(auth.OrganizationPermissions, auth.RepositoryPermissions)
}

// ValidateRole checks a custom role's definition and returns it normalized: the name and description trimmed, the
// permissions sorted and deduplicated.
func ValidateRole(in RoleInput) (RoleInput, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	switch {
	case in.Name == "" || len(in.Name) > 50:
		return in, &ValidationError{Field: "name", Code: "role.name_invalid", Message: "a role's name is 1 to 50 characters"}
	case len(in.Description) > 500:
		return in, &ValidationError{Field: "description", Code: "role.description_invalid", Message: "a role's description is at most 500 characters"}
	case len(in.Permissions) == 0:
		return in, &ValidationError{Field: "permissions", Code: "role.permissions_invalid", Message: "a role holds at least one permission"}
	}
	allowed := RolePermissions()
	for _, p := range in.Permissions {
		if !slices.Contains(allowed, p) {
			return in, &ValidationError{Field: "permissions", Code: "role.permissions_invalid", Message: p + " is not a permission a role holds at an organization"}
		}
	}
	in.Permissions = slices.Compact(slices.Sorted(slices.Values(in.Permissions)))
	return in, nil
}

// Member is a user who belongs to an organization, with the roles they hold there.
type Member struct {
	UserID      uuid.UUID
	DisplayName string
	JoinedAt    time.Time
	Roles       []Role
}

// State is where an invitation or a join request stands.
type State string

// The states. An invitation ends accepted, declined (by its invitee) or cancelled (by a manager); a join request
// approved or declined. Expired rows are deleted.
const (
	StatePending   State = "pending"
	StateAccepted  State = "accepted"
	StateDeclined  State = "declined"
	StateCancelled State = "cancelled"
	StateApproved  State = "approved"
)

// Invitation names an email; only a user who holds it, verified, accepts it.
type Invitation struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// OrgSlug and OrgName name the organization, for the invitee.
	OrgSlug, OrgName string
	Email            string
	RoleID           uuid.UUID
	// RoleName names the role, for whoever reads the invitation; empty once a custom role is gone.
	RoleName  string
	InvitedBy uuid.UUID
	State     State
	CreatedAt time.Time
	ExpiresAt time.Time
}

// JoinRequest is a user's request to join an organization, waiting for one of its member managers.
type JoinRequest struct {
	ID    uuid.UUID
	OrgID uuid.UUID
	// OrgSlug and OrgName name the organization, for the requester.
	OrgSlug, OrgName string
	UserID           uuid.UUID
	// UserName is the requester's display name, for the managers.
	UserName  string
	Note      string
	State     State
	CreatedAt time.Time
	ExpiresAt time.Time
}

// The errors the domain answers; an adapter maps each to its status.
var (
	// ErrNotFound is an organization, member, invitation or request that isn't there, or one the viewer may not see.
	ErrNotFound = errors.New("not found")
	// ErrSlugTaken is a slug another organization has or had.
	ErrSlugTaken = errors.New("another organization has this slug")
	// ErrAlreadyMember is a user who already belongs to the organization.
	ErrAlreadyMember = errors.New("already a member")
	// ErrNotMember is a user who doesn't belong to the organization.
	ErrNotMember = errors.New("not a member")
	// ErrInvited is an email that already has a pending invitation to the organization.
	ErrInvited = errors.New("this email is already invited")
	// ErrRequested is a user who already asked to join the organization.
	ErrRequested = errors.New("already asked to join")
	// ErrLastOwner is the one Owner an organization has, who can't leave or be removed.
	ErrLastOwner = errors.New("the organization's last Owner can't leave")
	// ErrInviterGone is an invitation whose inviter no longer may add members, so it adds nobody.
	ErrInviterGone = errors.New("whoever invited you may no longer add members")
	// ErrNotOrganizationRole is a role that isn't granted at the organization: not built in, and not its own.
	ErrNotOrganizationRole = errors.New("not a role of this organization")
	// ErrRoleNameTaken is a name another role granted at the organization has.
	ErrRoleNameTaken = errors.New("another role here has this name")
	// ErrBuiltInRole is a built-in role, which nobody changes or deletes.
	ErrBuiltInRole = errors.New("a built-in role can't be changed")
	// ErrRoleInUse is a custom role somebody still holds, or that the organization gives everyone who joins.
	ErrRoleInUse = errors.New("the role is still granted, or is the membership role")
	// ErrGranted is a role the member already holds.
	ErrGranted = errors.New("the member already holds this role")
)

// ValidationError is input the domain refuses: Field names what, Code why, in the API's terms.
type ValidationError struct {
	Field, Code, Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// slugPattern is 2 to 39 lowercase letters, digits and single hyphens, neither first nor last.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ReservedSlugs are the Console's own paths, which no organization may take.
var ReservedSlugs = []string{
	"api", "auth", "graphql", "new", "settings", "sign-in", "sign-out", "sign-up", "join", "invitations", "me", "admin",
	"about", "help", "docs", "static", "assets", "healthz", "readyz",
}

// ValidateSlug reports whether slug may name an organization.
func ValidateSlug(slug string) error {
	switch {
	case len(slug) < 2 || len(slug) > 39 || !slugPattern.MatchString(slug):
		return &ValidationError{Field: "slug", Code: "slug.invalid", Message: "a slug is 2 to 39 lowercase letters, digits and single hyphens"}
	case slices.Contains(ReservedSlugs, slug):
		return &ValidationError{Field: "slug", Code: "slug.reserved", Message: "this slug is one of the Console's own paths"}
	}
	return nil
}

// ValidateName reports whether name may name an organization.
func ValidateName(name string) error {
	if n := strings.TrimSpace(name); n == "" || len(n) > 100 {
		return &ValidationError{Field: "name", Code: "name.invalid", Message: "a name is 1 to 100 characters"}
	}
	return nil
}

// ValidateEmail reports whether email can be invited: something@somewhere, lowercase.
func ValidateEmail(email string) error {
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n") || len(email) > 254 || email != strings.ToLower(email) {
		return &ValidationError{Field: "email", Code: "email.invalid", Message: "not an email address"}
	}
	return nil
}
