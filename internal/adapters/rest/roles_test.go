package rest

import (
	"slices"
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

func TestRoleListing(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "roles", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/roles"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Roles []Role }](t, body); len(got.Roles) != 4 || got.Roles[0].Name != "Owner" || got.Roles[0].OrgId != nil {
					t.Errorf("roles = %s", body)
				}
			}},
		{name: "roles of an organization the viewer isn't in", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/roles"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "permissions", viewer: maxim, method: "GET", path: fixed("/v1/permissions?scope=organization"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Permissions []string }](t, body); !slices.Equal(got.Permissions, orgs.RolePermissions()) {
					t.Errorf("permissions = %s", body)
				}
			}},
		{name: "the deprecated built-in roles", viewer: maxim, method: "GET", path: fixed("/v1/roles?scope=organization"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Roles []Role }](t, body); len(got.Roles) != 4 || got.Roles[0].Name != "Owner" {
					t.Errorf("roles = %s", body)
				}
			}},
		{name: "permissions at another scope", viewer: maxim, method: "GET", path: fixed("/v1/permissions?scope=repository"), wantStatus: 400, wantCode: "request.invalid"},
	})
}

func TestRolesAPI(t *testing.T) {
	manager := orgstest.Manager.ID.String()
	runOrgCases(t, []orgCase{
		{name: "define a role", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/roles"), body: `{"name":"Releaser","description":"Merges.","permissions":["repo.merge","org.read"]}`, wantStatus: 201,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if r := decode[Role](t, body); r.Name != "Releaser" || r.BuiltIn || r.OrgId == nil || *r.OrgId != f.acme.ID || len(r.Permissions) != 2 || r.Permissions[0] != "org.read" {
					t.Errorf("role = %s", body)
				}
			}},
		{name: "define a role as a Member", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/roles"), body: `{"name":"Releaser","permissions":["org.read"]}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "define a role with a permission from another scope", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/roles"), body: `{"name":"Releaser","permissions":["orgs.create"]}`, wantStatus: 400, wantCode: "role.permissions_invalid"},
		{name: "define a role without permissions", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/roles"), body: `{"name":"Releaser"}`, wantStatus: 400, wantCode: "role.permissions_invalid"},
		{name: "define a role with a taken name", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/roles"), body: `{"name":"manager","permissions":["org.read"]}`, wantStatus: 409, wantCode: "role.name_taken"},
		{name: "change a role", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/roles/" + manager), body: `{"description":"Manages members."}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[Role](t, body); r.Name != "Manager" || r.Description != "Manages members." || len(r.Permissions) != 2 {
					t.Errorf("role = %s", body)
				}
			}},
		{name: "change a built-in role", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/roles/" + auth.RoleGuest.String()), body: `{"name":"Visitor"}`, wantStatus: 409, wantCode: "role.built_in"},
		{name: "change a role that isn't", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme/roles/00000000-0000-7000-8000-00000000ffff"), body: `{"name":"Visitor"}`, wantStatus: 404, wantCode: "role.not_found"},
		{name: "delete a role", viewer: olivia, method: "DELETE", path: fixed("/v1/orgs/acme/roles/" + manager), wantStatus: 204},
		{name: "delete a built-in role", viewer: olivia, method: "DELETE", path: fixed("/v1/orgs/acme/roles/" + auth.RoleMember.String()), wantStatus: 409, wantCode: "role.built_in"},
		{name: "grant a role", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles" }, body: `{"roleId":"` + manager + `"}`, wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if m, _ := f.store.Member(t.Context(), f.acme.ID, f.max.User.ID); len(m.Roles) != 2 {
					t.Errorf("Max holds %+v, want Member and Manager", m.Roles)
				}
			}},
		{name: "grant a held role", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles" }, body: `{"roleId":"` + auth.RoleMember.String() + `"}`, wantStatus: 409, wantCode: "grant.exists"},
		{name: "grant as a Member", viewer: maxim, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles" }, body: `{"roleId":"` + manager + `"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "grant to an outsider", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.nia.User.ID.String() + "/roles" }, body: `{"roleId":"` + manager + `"}`, wantStatus: 404, wantCode: "member.not_found"},
		{name: "grant a role that isn't", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles" }, body: `{"roleId":"00000000-0000-7000-8000-00000000ffff"}`, wantStatus: 400, wantCode: "role.invalid"},
		{name: "revoke a role", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string {
			return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles/" + auth.RoleMember.String()
		}, wantStatus: 204},
		{name: "revoke a role not held", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string {
			return "/v1/orgs/acme/members/" + f.max.User.ID.String() + "/roles/" + manager
		}, wantStatus: 404, wantCode: "grant.not_found"},
		{name: "revoke the last Owner's role", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string {
			return "/v1/orgs/acme/members/" + f.olivia.User.ID.String() + "/roles/" + auth.RoleOwner.String()
		}, wantStatus: 409, wantCode: "org.last_owner"},
	})
}

func TestDeprecatedRolesRouteHeaders(t *testing.T) {
	f := newOrgFixture(t)
	rec := f.call(&f.max, "GET", "/v1/roles?scope=organization", "")
	if rec.Code != 200 || rec.Header().Get("Deprecation") != rolesDeprecation || rec.Header().Get("Sunset") != rolesSunset {
		t.Errorf("status %d, Deprecation %q, Sunset %q", rec.Code, rec.Header().Get("Deprecation"), rec.Header().Get("Sunset"))
	}
}
