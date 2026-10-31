-- name: InsertOrganization :exec
INSERT INTO organizations (id, slug, name, membership_role_id, accepts_join_requests, providers, created_at)
VALUES (@id, @slug, @name, @membership_role_id, @accepts_join_requests, @providers, @created_at);

-- InsertSlug claims a slug for an organization; the primary key refuses one another organization has or had.
-- name: InsertSlug :exec
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2);

-- OrganizationBySlug finds an organization by any slug it has had.
-- name: OrganizationBySlug :one
SELECT sqlc.embed(o) FROM organizations o JOIN organization_slugs s ON s.org_id = o.id WHERE s.slug = $1;

-- name: OrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: UpdateOrganization :execrows
UPDATE organizations SET slug = @slug, name = @name, membership_role_id = @membership_role_id,
    accepts_join_requests = @accepts_join_requests, providers = @providers
WHERE id = @id;

-- OrganizationsOf lists the organizations a user belongs to, by name.
-- name: OrganizationsOf :many
SELECT sqlc.embed(o) FROM organizations o JOIN memberships m ON m.org_id = o.id WHERE m.user_id = $1 ORDER BY o.name, o.slug;

-- name: InsertMembership :exec
INSERT INTO memberships (org_id, user_id, joined_at) VALUES ($1, $2, $3);

-- name: DeleteMembership :execrows
DELETE FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: Membership :one
SELECT * FROM memberships WHERE org_id = $1 AND user_id = $2;

-- Members lists an organization's members, by display name.
-- name: Members :many
SELECT m.user_id, m.joined_at, p.display_name FROM memberships m JOIN principals p ON p.id = m.user_id
WHERE m.org_id = $1 ORDER BY p.display_name, m.user_id;

-- MemberByEmail finds the member who holds the email, verified.
-- name: MemberByEmail :one
SELECT m.user_id FROM memberships m JOIN principal_emails e ON e.principal_id = m.user_id
WHERE m.org_id = $1 AND e.email = $2 AND e.verified;

-- OrganizationGrants lists the roles every principal holds at the organization.
-- name: OrganizationGrants :many
SELECT g.principal_id, sqlc.embed(r) FROM grants g JOIN roles r ON r.id = g.role_id
WHERE g.scope = 'organization' AND g.scope_id = @org_id::uuid ORDER BY g.principal_id, r.name;

-- InsertGrant gives the principal the role at the scope, once: a grant it has is kept as it is.
-- name: InsertGrant :execrows
INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING;

-- DeleteScopeGrants takes every role the principal holds at the scope.
-- name: DeleteScopeGrants :execrows
DELETE FROM grants WHERE principal_id = @principal_id AND scope = @scope AND scope_id = @scope_id::uuid;

-- CountRoleHolders counts the principals holding the role at the organization.
-- name: CountRoleHolders :one
SELECT count(*) FROM grants WHERE role_id = @role_id AND scope = 'organization' AND scope_id = @org_id::uuid;

-- Permissions lists the permissions the principal's roles hold at the scope: the installation when scope_id is NULL.
-- name: Permissions :many
SELECT DISTINCT unnest(r.permissions)::text AS permission FROM grants g JOIN roles r ON r.id = g.role_id
WHERE g.principal_id = @principal_id AND g.scope = @scope AND g.scope_id IS NOT DISTINCT FROM sqlc.narg(scope_id)::uuid
ORDER BY 1;

-- name: Role :one
SELECT * FROM roles WHERE id = $1;

-- ClaimSlug gives the organization the slug, or changes nothing when another organization has or had it: zero rows.
-- name: ClaimSlug :execrows
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2)
ON CONFLICT (slug) DO UPDATE SET org_id = EXCLUDED.org_id WHERE organization_slugs.org_id = EXCLUDED.org_id;

-- LockOrganization holds the organization's row until the transaction ends, so changes to who may act there
-- happen one at a time.
-- name: LockOrganization :one
SELECT id FROM organizations WHERE id = $1 FOR UPDATE;

-- name: HoldsRole :one
SELECT EXISTS (SELECT 1 FROM grants WHERE principal_id = $1 AND role_id = $2 AND scope = 'organization' AND scope_id = @org_id::uuid);
