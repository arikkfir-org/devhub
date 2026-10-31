package orgs_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// granter is a custom role of Acme that manages grants but holds few permissions, so the grant rules bite.
var granter = orgs.Role{ID: uuid.MustParse("00000000-0000-7000-8000-000000000f02"), Name: "Granter", Permissions: []string{"org.read", "org.grants.manage"}}

// withGranter adds Granter to Acme's roles and makes Gwen hold it, beside Guest.
func (f *fixture) withGranter() {
	role := granter
	role.OrgID = f.acme.ID
	f.store.RoleList = append(f.store.RoleList, role)
	f.store.Grant(f.gwen.ID, role.ID, auth.ScopeOrganization, f.acme.ID)
}

// second drops a result and keeps its error.
func second[T any](_ T, err error) error { return err }

// releaseManager is a custom role's definition, untidy as a client sends it.
var releaseManager = orgs.RoleInput{Name: " Release manager ", Description: "Merges and bypasses.", Permissions: []string{"repo.merge", "org.read", "repo.merge", "repo.bypass"}}

// TestRoleLifecycle defines, changes and deletes a custom role as an Owner.
func TestRoleLifecycle(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	{
		role, err := f.service.CreateRole(ctx, f.olivia, f.acme.ID, releaseManager)
		if err != nil || role.Name != "Release manager" || role.OrgID != f.acme.ID || !slices.Equal(role.Permissions, []string{"org.read", "repo.bypass", "repo.merge"}) {
			t.Fatalf("CreateRole() = %+v, %v", role, err)
		}
		roles, err := f.service.Roles(ctx, f.max, f.acme.ID)
		if err != nil || len(roles) != 5 || roles[4].ID != role.ID {
			t.Errorf("Roles() = %+v, %v, want the new role last", roles, err)
		}
		if roles, _ := f.service.Roles(ctx, f.max, f.globex.ID); len(roles) != 4 {
			t.Errorf("Globex lists %d roles, want Acme's custom role kept out", len(roles))
		}
		updated, err := f.service.UpdateRole(ctx, f.olivia, f.acme.ID, role.ID, orgs.RoleInput{Name: "Releaser", Permissions: []string{"repo.merge"}})
		if err != nil || updated.Name != "Releaser" || !slices.Equal(updated.Permissions, []string{"repo.merge"}) || updated.ID != role.ID {
			t.Errorf("UpdateRole() = %+v, %v", updated, err)
		}
		if err := f.service.DeleteRole(ctx, f.olivia, f.acme.ID, role.ID); err != nil {
			t.Errorf("DeleteRole() error = %v", err)
		}
		if roles, _ := f.service.Roles(ctx, f.max, f.acme.ID); len(roles) != 4 {
			t.Errorf("%d roles remain, want 4", len(roles))
		}
		want := []string{"role.created", "role.updated", "role.deleted"}
		if !slices.Equal(f.store.Events, want) {
			t.Errorf("events = %v, want %v", f.store.Events, want)
		}
	}
}

// roleCase is one refusal or acceptance of a role operation: wantErr the domain error, or code a validation code.
type roleCase struct {
	name    string
	run     func(f *fixture) error
	wantErr error
	code    string
}

func runRoleCases(t *testing.T, tests []roleCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			err := tt.run(f)
			var verr *orgs.ValidationError
			switch {
			case tt.code != "":
				if !errors.As(err, &verr) || verr.Code != tt.code {
					t.Errorf("error = %v, want code %s", err, tt.code)
				}
			case !errors.Is(err, tt.wantErr):
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefiningRoles(t *testing.T) {
	ctx := t.Context()
	in := releaseManager
	runRoleCases(t, []roleCase{
		{name: "a member manager may not define roles", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.mia, f.acme.ID, in))
		}, wantErr: auth.ErrForbidden},
		{name: "an outsider sees no organization", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.nia, f.acme.ID, in))
		}, wantErr: orgs.ErrNotFound},
		{name: "nobody defines a role with permissions they lack", run: func(f *fixture) error {
			f.withGranter()
			return second(f.service.CreateRole(ctx, f.gwen, f.acme.ID, in))
		}, wantErr: auth.ErrForbidden},
		{name: "a granter defines a role within what they hold", run: func(f *fixture) error {
			f.withGranter()
			return second(f.service.CreateRole(ctx, f.gwen, f.acme.ID, orgs.RoleInput{Name: "Reader", Permissions: []string{"org.read"}}))
		}},
		{name: "a built-in name is taken, in any case", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.olivia, f.acme.ID, orgs.RoleInput{Name: "owner", Permissions: []string{"org.read"}}))
		}, wantErr: orgs.ErrRoleNameTaken},
		{name: "no permissions", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.olivia, f.acme.ID, orgs.RoleInput{Name: "Nothing"}))
		}, code: "role.permissions_invalid"},
		{name: "an installation permission", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.olivia, f.acme.ID, orgs.RoleInput{Name: "Creator", Permissions: []string{"orgs.create"}}))
		}, code: "role.permissions_invalid"},
		{name: "a blank name", run: func(f *fixture) error {
			return second(f.service.CreateRole(ctx, f.olivia, f.acme.ID, orgs.RoleInput{Name: "  ", Permissions: []string{"org.read"}}))
		}, code: "role.name_invalid"},
	})
}

func TestChangingRoles(t *testing.T) {
	ctx := t.Context()
	runRoleCases(t, []roleCase{
		{name: "a built-in role can't change", run: func(f *fixture) error {
			return second(f.service.UpdateRole(ctx, f.olivia, f.acme.ID, auth.RoleGuest, orgs.RoleInput{Name: "Visitor", Permissions: []string{"org.read"}}))
		}, wantErr: orgs.ErrBuiltInRole},
		{name: "a built-in role can't go", run: func(f *fixture) error {
			return f.service.DeleteRole(ctx, f.olivia, f.acme.ID, auth.RoleGuest)
		}, wantErr: orgs.ErrBuiltInRole},
		{name: "another organization's role isn't here", run: func(f *fixture) error {
			f.withGranter()
			return f.service.DeleteRole(ctx, f.max, f.globex.ID, granter.ID)
		}, wantErr: orgs.ErrNotFound},
		{name: "changing a role takes its old permissions too", run: func(f *fixture) error {
			f.withGranter()
			f.store.Grant(f.gwen.ID, orgstest.Manager.ID, auth.ScopeOrganization, f.acme.ID)
			// Gwen holds org.members.manage through Manager, but not what Granter would lose.
			return second(f.service.UpdateRole(ctx, f.gwen, f.acme.ID, orgstest.Manager.ID, orgs.RoleInput{Name: "Manager", Permissions: []string{"org.read"}}))
		}},
		{name: "a role somebody holds stays", run: func(f *fixture) error {
			return f.service.DeleteRole(ctx, f.olivia, f.acme.ID, orgstest.Manager.ID)
		}, wantErr: orgs.ErrRoleInUse},
		{name: "the membership role stays", run: func(f *fixture) error {
			f.withGranter()
			org := f.acme
			org.MembershipRoleID = granter.ID
			f.store.Orgs[org.ID] = org
			f.store.Grants = slices.DeleteFunc(f.store.Grants, func(g orgstest.Grant) bool { return g.Role == granter.ID })
			return f.service.DeleteRole(ctx, f.olivia, f.acme.ID, granter.ID)
		}, wantErr: orgs.ErrRoleInUse},
	})
}

func TestGrantsAndRevocations(t *testing.T) {
	ctx := t.Context()
	t.Run("an Owner grants and revokes", func(t *testing.T) {
		testGrantAndRevoke(t, newFixture(t))
	})
	t.Run("the organization keeps an Owner", func(t *testing.T) {
		testKeepsAnOwner(t, newFixture(t))
	})
	tests := []struct {
		name    string
		run     func(f *fixture) error
		wantErr error
	}{
		{name: "a member manager may not grant", run: func(f *fixture) error {
			return f.service.GrantRole(ctx, f.mia, f.acme.ID, f.max.ID, auth.RoleGuest)
		}, wantErr: auth.ErrForbidden},
		{name: "a granter grants nothing beyond what they hold", run: func(f *fixture) error {
			f.withGranter()
			return f.service.GrantRole(ctx, f.gwen, f.acme.ID, f.max.ID, auth.RoleOwner)
		}, wantErr: auth.ErrForbidden},
		{name: "a granter revokes nothing beyond what they hold", run: func(f *fixture) error {
			f.withGranter()
			return f.service.RevokeRole(ctx, f.gwen, f.acme.ID, f.mia.ID, orgstest.Manager.ID)
		}, wantErr: auth.ErrForbidden},
		{name: "a granter grants within what they hold", run: func(f *fixture) error {
			f.withGranter()
			return f.service.GrantRole(ctx, f.gwen, f.acme.ID, f.max.ID, auth.RoleGuest)
		}},
		{name: "an outsider gets nothing", run: func(f *fixture) error {
			return f.service.GrantRole(ctx, f.olivia, f.acme.ID, f.nia.ID, auth.RoleGuest)
		}, wantErr: orgs.ErrNotFound},
		{name: "an outsider sees no organization", run: func(f *fixture) error {
			return f.service.GrantRole(ctx, f.nia, f.acme.ID, f.max.ID, auth.RoleGuest)
		}, wantErr: orgs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if err := tt.run(f); !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// testGrantAndRevoke grants and revokes as an Owner.
func testGrantAndRevoke(t *testing.T, f *fixture) {
	t.Helper()
	ctx := t.Context()
	if err := f.service.GrantRole(ctx, f.olivia, f.acme.ID, f.max.ID, orgstest.Manager.ID); err != nil {
		t.Fatalf("GrantRole() error = %v", err)
	}
	m, _ := f.store.Member(ctx, f.acme.ID, f.max.ID)
	if len(m.Roles) != 2 {
		t.Errorf("Max holds %+v, want Member and Manager", m.Roles)
	}
	if err := f.service.GrantRole(ctx, f.olivia, f.acme.ID, f.max.ID, orgstest.Manager.ID); !errors.Is(err, orgs.ErrGranted) {
		t.Errorf("granting again: error = %v, want granted", err)
	}
	if err := f.service.RevokeRole(ctx, f.olivia, f.acme.ID, f.max.ID, auth.RoleMember); err != nil {
		t.Errorf("RevokeRole() error = %v", err)
	}
	if err := f.service.RevokeRole(ctx, f.olivia, f.acme.ID, f.max.ID, auth.RoleMember); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("revoking again: error = %v, want not found", err)
	}
	want := []string{"role.granted", "role.revoked"}
	if !slices.Equal(f.store.Events, want) {
		t.Errorf("events = %v, want %v", f.store.Events, want)
	}
}

// testKeepsAnOwner checks that the Owner role stays with the organization's last Owner.
func testKeepsAnOwner(t *testing.T, f *fixture) {
	t.Helper()
	ctx := t.Context()
	if err := f.service.RevokeRole(ctx, f.olivia, f.acme.ID, f.olivia.ID, auth.RoleOwner); !errors.Is(err, orgs.ErrLastOwner) {
		t.Fatalf("revoking the last Owner: error = %v, want last owner", err)
	}
	if err := f.service.GrantRole(ctx, f.olivia, f.acme.ID, f.max.ID, auth.RoleOwner); err != nil {
		t.Fatalf("GrantRole(Owner) error = %v", err)
	}
	if err := f.service.RevokeRole(ctx, f.olivia, f.acme.ID, f.olivia.ID, auth.RoleOwner); err != nil {
		t.Errorf("revoking one of two Owners: error = %v", err)
	}
}
