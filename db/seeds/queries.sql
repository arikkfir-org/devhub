-- name: InsertUser :exec
INSERT INTO principals (id, kind, display_name) VALUES ($1, 'user', $2);

-- name: InsertVerifiedEmail :exec
INSERT INTO principal_emails (email, principal_id, verified) VALUES ($1, $2, true);

-- name: InsertSeedIdentity :exec
INSERT INTO identities (issuer, subject, principal_id, email, email_verified, name)
VALUES (@issuer, @subject, @principal_id, @email, true, @name);

-- name: InsertSeedGrant :exec
INSERT INTO grants (principal_id, role_id, scope, scope_id, source) VALUES (@principal_id, @role_id, @scope, @scope_id, 'grant');

-- name: InsertSeedOrganization :exec
INSERT INTO organizations (id, slug, name, membership_role_id) VALUES (@id, @slug, @slug_name, @membership_role_id);

-- name: InsertSeedSlug :exec
INSERT INTO organization_slugs (slug, org_id) VALUES ($1, $2);

-- name: InsertSeedMembership :exec
INSERT INTO memberships (org_id, user_id) VALUES ($1, $2);

-- name: InsertSeedInvitation :exec
INSERT INTO invitations (id, org_id, email, role_id, invited_by, expires_at)
VALUES (@id, @org_id, @email, @role_id, @invited_by, @expires_at);

-- name: InsertSeedRole :exec
INSERT INTO roles (id, scope, name, description, permissions, built_in, org_id)
VALUES (@id, 'organization', @name, @description, string_to_array(@permissions::text, ','), false, @org_id);
