package graphql

import (
	"testing"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs/orgstest"
)

func TestRoleMutations(t *testing.T) {
	manager := orgstest.Manager.ID.String()
	runOrgCases(t, []orgCase{
		{name: "define a role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createRole(organizationId: "` + f.acme.ID.String() + `", input: {name: "Releaser", description: "Merges.", permissions: ["repo.merge", "org.read"]}) { name builtIn organizationId permissions } }`
		}, want: []string{`"name":"Releaser","builtIn":false,"organizationId":"`, `"permissions":["org.read","repo.merge"]`}},
		{name: "define a role as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { createRole(organizationId: "` + f.acme.ID.String() + `", input: {name: "Releaser", permissions: ["org.read"]}) { name } }`
		}, wantCode: forbidden},
		{name: "define a role with a bad permission", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { createRole(organizationId: "` + f.acme.ID.String() + `", input: {name: "Releaser", permissions: ["orgs.create"]}) { name } }`
		}, wantCode: badUserInput, wantReason: "role.permissions_invalid"},
		{name: "change a role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { updateRole(organizationId: "` + f.acme.ID.String() + `", id: "` + manager + `", input: {name: "Member manager", permissions: ["org.read", "org.members.manage"]}) { name } }`
		}, want: []string{`"name":"Member manager"`}},
		{name: "change a built-in role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { updateRole(organizationId: "` + f.acme.ID.String() + `", id: "` + auth.RoleGuest.String() + `", input: {name: "Visitor", permissions: ["org.read"]}) { name } }`
		}, wantCode: conflict, wantReason: "role.built_in"},
		{name: "delete a role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { deleteRole(organizationId: "` + f.acme.ID.String() + `", id: "` + manager + `") }`
		}, want: []string{`{"data":{"deleteRole":true}}`}},
		{name: "grant a role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { grantRole(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.max.User.ID.String() + `", roleId: "` + manager + `") }`
		}, want: []string{`{"data":{"grantRole":true}}`}},
		{name: "grant a held role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { grantRole(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.max.User.ID.String() + `", roleId: "` + auth.RoleMember.String() + `") }`
		}, wantCode: conflict, wantReason: "grant.exists"},
		{name: "grant as a member", viewer: maxim, document: func(f *orgFixture) string {
			return `mutation { grantRole(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.max.User.ID.String() + `", roleId: "` + manager + `") }`
		}, wantCode: forbidden},
		{name: "revoke a role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { revokeRole(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.max.User.ID.String() + `", roleId: "` + auth.RoleMember.String() + `") }`
		}, want: []string{`{"data":{"revokeRole":true}}`}},
		{name: "revoke the last Owner's role", viewer: olivia, document: func(f *orgFixture) string {
			return `mutation { revokeRole(organizationId: "` + f.acme.ID.String() + `", userId: "` + f.olivia.User.ID.String() + `", roleId: "` + auth.RoleOwner.String() + `") }`
		}, wantCode: conflict, wantReason: "org.last_owner"},
		{name: "the deprecated built-in roles", viewer: maxim, document: fixed(`{ roles(scope: ORGANIZATION) { name } }`), want: []string{`{"name":"Owner"}`, `{"name":"Manager"}`}},
		{name: "revoke with nonsense ids", viewer: olivia, document: fixed(`mutation { revokeRole(organizationId: "x", userId: "y", roleId: "z") }`), wantCode: notFound, wantReason: "role.not_found"},
	})
}
