-- Custom roles: a role an organization defines for itself, beside the built-in ones
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md, "Roles and
-- grants"). A custom role carries its organization's org_id and is granted there alone; a built-in role carries none.
-- +goose Up

ALTER TABLE roles ADD COLUMN org_id uuid REFERENCES organizations (id) ON DELETE CASCADE;
ALTER TABLE roles ADD CONSTRAINT roles_org_or_built_in CHECK (built_in = (org_id IS NULL));
ALTER TABLE roles DROP CONSTRAINT roles_scope_name_key;
-- A name is unique among the built-in roles of a scope, and among one organization's custom roles.
CREATE UNIQUE INDEX roles_name ON roles (org_id, scope, name) NULLS NOT DISTINCT;
CREATE INDEX roles_org ON roles (org_id);

-- An invitation keeps the ID of the role it offered, as events do, even once a custom role is gone: deleting a role
-- cancels the invitations that still offer it.
ALTER TABLE invitations DROP CONSTRAINT IF EXISTS invitations_role_id_fkey; -- gone already after a rollback

-- +goose Down
-- Rolling back deletes the custom roles, with their grants, and makes Member the membership role where a custom
-- role was: the previous schema has no room for them. Invitations keep their role IDs, and no foreign key comes back,
-- since cancelled invitations may name roles that are gone.
UPDATE organizations SET membership_role_id = '00000000-0000-7000-8000-000000000202'
WHERE membership_role_id IN (SELECT id FROM roles WHERE NOT built_in);
DELETE FROM grants WHERE role_id IN (SELECT id FROM roles WHERE NOT built_in);
DELETE FROM roles WHERE NOT built_in;
DROP INDEX roles_org;
DROP INDEX roles_name;
ALTER TABLE roles ADD CONSTRAINT roles_scope_name_key UNIQUE (scope, name);
ALTER TABLE roles DROP CONSTRAINT roles_org_or_built_in;
ALTER TABLE roles DROP COLUMN org_id;
