-- OrganizationRoles lists the roles granted at the organization: the built-in organization roles first, then its own.
-- name: OrganizationRoles :many
SELECT * FROM roles WHERE scope = 'organization' AND (org_id IS NULL OR org_id = @org_id::uuid) ORDER BY built_in DESC, name;

-- name: InsertRole :exec
INSERT INTO roles (id, scope, name, description, permissions, built_in, org_id)
VALUES (@id, 'organization', @name, @description, @permissions, false, @org_id);

-- UpdateRole rewrites one of the organization's own roles; a built-in role changes nothing.
-- name: UpdateRole :execrows
UPDATE roles SET name = @name, description = @description, permissions = @permissions
WHERE id = @id AND org_id = @org_id::uuid AND NOT built_in;

-- name: DeleteRole :execrows
DELETE FROM roles WHERE id = $1 AND org_id = @org_id::uuid AND NOT built_in;

-- RoleInUse says whether anyone holds the role or an organization gives it to everyone who joins.
-- name: RoleInUse :one
SELECT (EXISTS (SELECT 1 FROM grants WHERE role_id = $1) OR EXISTS (SELECT 1 FROM organizations WHERE membership_role_id = $1))::boolean;

-- CancelInvitationsOfRole cancels the organization's pending invitations that offer the role, and names each.
-- name: CancelInvitationsOfRole :many
UPDATE invitations SET state = 'cancelled', decided_at = @decided_at, decided_by = @decided_by
WHERE org_id = @org_id AND role_id = @role_id AND state = 'pending' RETURNING id, email;

-- DeleteOrganizationGrant takes one role from the principal at the organization.
-- name: DeleteOrganizationGrant :execrows
DELETE FROM grants WHERE principal_id = $1 AND role_id = $2 AND scope = 'organization' AND scope_id = @org_id::uuid;
