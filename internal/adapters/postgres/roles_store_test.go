package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/db/migrations"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

func TestOrgStoreCustomRoles(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	release := orgs.Role{ID: uuid.New(), Name: "Release manager", Description: "Merges.", Permissions: []string{"org.read", "repo.merge"}, OrgID: f.acme.ID}
	if err := f.s.CreateRole(ctx, f.olivia, release); err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if err := f.s.CreateRole(ctx, f.olivia, orgs.Role{ID: uuid.New(), Name: "Release manager", Permissions: []string{"org.read"}, OrgID: f.acme.ID}); !errors.Is(err, orgs.ErrRoleNameTaken) {
		t.Errorf("CreateRole() with the same name: error = %v, want name taken", err)
	}
	globex := orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleMember, CreatedAt: now}
	if err := f.s.Create(ctx, f.nia, globex); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CreateRole(ctx, f.nia, orgs.Role{ID: uuid.New(), Name: "Release manager", Permissions: []string{"org.read"}, OrgID: globex.ID}); err != nil {
		t.Errorf("another organization's role with the same name: error = %v", err)
	}
	roles, err := f.s.Roles(ctx, f.acme.ID)
	if err != nil || len(roles) != 4 || roles[3].ID != release.ID || roles[3].OrgID != f.acme.ID || roles[3].BuiltIn {
		t.Fatalf("Roles(acme) = %+v, %v, want the built-in roles then Release manager", roles, err)
	}
	if r, err := f.s.Role(ctx, release.ID); err != nil || r.OrgID != f.acme.ID || !slices.Equal(r.Permissions, release.Permissions) {
		t.Errorf("Role() = %+v, %v", r, err)
	}
	release.Name, release.Permissions = "Releaser", []string{"org.read", "repo.merge", "repo.bypass"}
	if err := f.s.UpdateRole(ctx, f.olivia, release); err != nil {
		t.Errorf("UpdateRole() error = %v", err)
	}
	if err := f.s.UpdateRole(ctx, f.olivia, orgs.Role{ID: auth.RoleGuest, Name: "Visitor", Permissions: []string{"org.read"}, OrgID: f.acme.ID}); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("UpdateRole(Guest) error = %v, want not found", err)
	}
	testGrantsAndDeletion(t, f, release)
}

// testGrantsAndDeletion grants and revokes the custom role, then deletes it once nobody holds it.
func testGrantsAndDeletion(t *testing.T, f *orgFixture, release orgs.Role) {
	t.Helper()
	ctx := context.Background()
	if err := f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, release.ID); err != nil {
		t.Fatalf("GrantRole() error = %v", err)
	}
	if err := f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, release.ID); !errors.Is(err, orgs.ErrGranted) {
		t.Errorf("GrantRole() again: error = %v, want granted", err)
	}
	if held, _ := f.s.Permissions(ctx, f.max, auth.ScopeOrganization, f.acme.ID); !slices.Contains(held, "repo.bypass") {
		t.Errorf("Max holds %v, want the updated role's repo.bypass", held)
	}
	if err := f.s.DeleteRole(ctx, f.olivia, f.acme.ID, release.ID, now); !errors.Is(err, orgs.ErrRoleInUse) {
		t.Errorf("DeleteRole() while granted: error = %v, want in use", err)
	}
	if err := f.s.RevokeRole(ctx, f.olivia, f.acme.ID, f.max, release.ID); err != nil {
		t.Fatalf("RevokeRole() error = %v", err)
	}
	if err := f.s.RevokeRole(ctx, f.olivia, f.acme.ID, f.max, release.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("RevokeRole() again: error = %v, want not found", err)
	}
	inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "nia@example.com", RoleID: release.ID, InvitedBy: f.olivia, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := f.s.Invite(ctx, f.olivia, inv); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.Invitation(ctx, inv.ID); err != nil || got.RoleName != "Releaser" {
		t.Errorf("Invitation() = %+v, %v, want its role named Releaser", got, err)
	}
	if err := f.s.DeleteRole(ctx, f.olivia, f.acme.ID, release.ID, now); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
	if got, err := f.s.Invitation(ctx, inv.ID); err != nil || got.State != orgs.StateCancelled || got.RoleName != "" {
		t.Errorf("the deleted role's invitation = %+v, %v, want cancelled, its role's name gone", got, err)
	}
	if roles, _ := f.s.Roles(ctx, f.acme.ID); len(roles) != 3 {
		t.Errorf("%d roles remain, want the built-in 3", len(roles))
	}
	inv.ID, inv.Email = uuid.New(), "late@example.com"
	if err := f.s.Invite(ctx, f.olivia, inv); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("Invite() with the deleted role: error = %v, want not found", err)
	}
	if err := f.s.DeleteRole(ctx, f.olivia, f.acme.ID, auth.RoleGuest, now); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("DeleteRole(Guest) error = %v, want not found", err)
	}
	want := []string{"role.created", "org.created", "member.joined", "role.created", "role.updated", "role.granted", "role.revoked", "invitation.made", "invitation.cancelled", "role.deleted"}
	if got := eventKinds(t, f.pool); len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("events = %v, want them to end with %v", got, want)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'roles' AND principal_id = $1", f.olivia); n != 3 {
		t.Errorf("%d audit rows on roles name Olivia, want 3: the insert, the update and the delete", n)
	}
}

func TestOrgStoreRevokeKeepsAnOwner(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	if err := f.s.RevokeRole(ctx, f.olivia, f.acme.ID, f.olivia, auth.RoleOwner); !errors.Is(err, orgs.ErrLastOwner) {
		t.Fatalf("revoking the last Owner: error = %v, want last owner", err)
	}
	if err := f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, auth.RoleOwner); err != nil {
		t.Fatal(err)
	}
	// Two Owners give up Owner at once: exactly one succeeds.
	errs := make(chan error, 2)
	for _, user := range []uuid.UUID{f.olivia, f.max} {
		go func() { errs <- f.s.RevokeRole(ctx, user, f.acme.ID, user, auth.RoleOwner) }()
	}
	var lastOwner, revoked int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			revoked++
		case errors.Is(err, orgs.ErrLastOwner):
			lastOwner++
		default:
			t.Errorf("RevokeRole() error = %v", err)
		}
	}
	if revoked != 1 || lastOwner != 1 {
		t.Errorf("revoked %d and refused %d, want one of each", revoked, lastOwner)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE role_id = $1 AND scope_id = $2", auth.RoleOwner, f.acme.ID); n != 1 {
		t.Errorf("%d Owners remain, want 1", n)
	}
}

// TestOrgStoreDeleteRoleAgainstGrant deletes a role while another transaction grants it, many times over: the
// organization's lock orders them, so either the grant lands and the deletion is refused, or the deletion lands and
// the grant finds no role. A grant that lands and then vanishes with the role is the failure.
func TestOrgStoreDeleteRoleAgainstGrant(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	for i := range 10 {
		role := orgs.Role{ID: uuid.New(), Name: "Temp " + uuid.NewString()[:8], Permissions: []string{"org.read"}, OrgID: f.acme.ID}
		if err := f.s.CreateRole(ctx, f.olivia, role); err != nil {
			t.Fatal(err)
		}
		deletes, grants := make(chan error, 1), make(chan error, 1)
		go func() { deletes <- f.s.DeleteRole(ctx, f.olivia, f.acme.ID, role.ID, now) }()
		go func() { grants <- f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, role.ID) }()
		deleted, granted := <-deletes, <-grants
		grantRows, roleRows := count(t, f.pool, "SELECT count(*) FROM grants WHERE role_id = $1", role.ID), count(t, f.pool, "SELECT count(*) FROM roles WHERE id = $1", role.ID)
		switch {
		case deleted == nil && errors.Is(granted, orgs.ErrNotFound) && grantRows == 0 && roleRows == 0:
		case errors.Is(deleted, orgs.ErrRoleInUse) && granted == nil && grantRows == 1 && roleRows == 1:
		default:
			t.Fatalf("round %d: delete = %v, grant = %v, %d grants and %d roles remain", i, deleted, granted, grantRows, roleRows)
		}
	}
}

// TestCustomRolesRollback rolls the custom roles' migration back over a database that holds one, granted and set as
// the membership role, then applies it again.
func TestCustomRolesRollback(t *testing.T) {
	f := newOrgFixture(t)
	ctx := context.Background()
	release := orgs.Role{ID: uuid.New(), Name: "Release manager", Permissions: []string{"org.read", "repo.merge"}, OrgID: f.acme.ID}
	if err := f.s.CreateRole(ctx, f.olivia, release); err != nil {
		t.Fatal(err)
	}
	if err := f.s.GrantRole(ctx, f.olivia, f.acme.ID, f.max, release.ID); err != nil {
		t.Fatal(err)
	}
	f.acme.MembershipRoleID = release.ID
	if err := f.s.Update(ctx, f.olivia, f.acme); err != nil {
		t.Fatal(err)
	}
	schema := newSchema(t, f.pool, migrations.FS, migrations.Go...)
	if _, err := schema.provider.DownTo(ctx, 4); err != nil {
		t.Fatalf("rolling back to version 4: %v", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM roles WHERE id = $1", release.ID); n != 0 {
		t.Error("the custom role survived the rollback")
	}
	if n := count(t, f.pool, "SELECT count(*) FROM grants WHERE principal_id = $1 AND scope_id = $2", f.max, f.acme.ID); n != 1 {
		t.Errorf("Max holds %d grants at Acme after the rollback, want Member alone", n)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM organizations WHERE id = $1 AND membership_role_id = $2", f.acme.ID, auth.RoleMember); n != 1 {
		t.Error("Acme's membership role isn't Member after the rollback")
	}
	if err := schema.Migrate(ctx); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if err := f.s.CreateRole(ctx, f.olivia, release); err != nil {
		t.Errorf("CreateRole() after the rollback and migration: %v", err)
	}
}
