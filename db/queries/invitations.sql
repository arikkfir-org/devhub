-- name: InsertInvitation :exec
INSERT INTO invitations (id, org_id, email, role_id, invited_by, created_at, expires_at)
VALUES (@id, @org_id, @email, @role_id, @invited_by, @created_at, @expires_at);

-- Invitation finds an invitation with its organization's slug and name, and its role's name while the role exists.
-- name: Invitation :one
SELECT sqlc.embed(i), o.slug, o.name, coalesce(r.name, '')::text AS role_name FROM invitations i
JOIN organizations o ON o.id = i.org_id LEFT JOIN roles r ON r.id = i.role_id WHERE i.id = $1;

-- PendingInvitations lists an organization's invitations that wait, oldest first.
-- name: PendingInvitations :many
SELECT sqlc.embed(i), o.slug, o.name, coalesce(r.name, '')::text AS role_name FROM invitations i
JOIN organizations o ON o.id = i.org_id LEFT JOIN roles r ON r.id = i.role_id
WHERE i.org_id = $1 AND i.state = 'pending' AND i.expires_at > @now::timestamptz ORDER BY i.created_at, i.id;

-- InvitationsFor lists the invitations that wait for any of the emails, oldest first.
-- name: InvitationsFor :many
SELECT sqlc.embed(i), o.slug, o.name, coalesce(r.name, '')::text AS role_name FROM invitations i
JOIN organizations o ON o.id = i.org_id LEFT JOIN roles r ON r.id = i.role_id
WHERE i.email = ANY(@emails::text[]) AND i.state = 'pending' AND i.expires_at > @now::timestamptz ORDER BY i.created_at, i.id;

-- CloseInvitation ends a pending invitation.
-- name: CloseInvitation :execrows
UPDATE invitations SET state = @state, decided_at = @decided_at, decided_by = @decided_by WHERE id = @id AND state = 'pending';

-- name: DeleteExpiredInvitations :execrows
DELETE FROM invitations WHERE state = 'pending' AND expires_at < @now::timestamptz;

-- DeleteUserInvitations drops the pending invitations to any of the user's verified emails at the organization:
-- they are moot once the user joined, and must not outlive their leaving.
-- name: DeleteUserInvitations :execrows
DELETE FROM invitations WHERE org_id = @org_id AND state = 'pending'
  AND email IN (SELECT email FROM principal_emails WHERE principal_id = @user_id AND verified);

-- DeleteExpiredInvitationTo drops an expired invitation to the email that would block a new one.
-- name: DeleteExpiredInvitationTo :execrows
DELETE FROM invitations WHERE org_id = $1 AND email = $2 AND state = 'pending' AND expires_at <= @now::timestamptz;
