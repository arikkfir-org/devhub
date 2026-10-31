package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// emptyOrgs returns the organizations' service over an empty store.
func emptyOrgs(t *testing.T) *orgs.Service {
	t.Helper()
	store := orgstest.NewStore()
	return orgs.NewService(store, auth.NewAuthorizer(store))
}

// orgFixture is Acme, with Olivia its Owner and Max a Member, Nia outside it with a pending invitation and
// permission to create organizations, over the organizations' service.
type orgFixture struct {
	store            *orgstest.Store
	service          *orgs.Service
	acme             orgs.Organization
	olivia, max, nia auth.Authenticated
	invitation       orgs.Invitation
}

func viewerOf(name, email string) auth.Authenticated {
	return auth.Authenticated{User: auth.User{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name},
		Emails: []auth.Email{{Email: email, Verified: true}}}}
}

func newOrgFixture(t *testing.T) *orgFixture {
	t.Helper()
	store := orgstest.NewStore()
	f := &orgFixture{store: store, service: orgs.NewService(store, auth.NewAuthorizer(store))}
	f.service.SetClock(func() time.Time { return now })
	f.olivia, f.max, f.nia = viewerOf("Olivia", "olivia@example.com"), viewerOf("Max", "max@example.com"), viewerOf("Nia", "nia@example.com")
	for _, v := range []auth.Authenticated{f.olivia, f.max, f.nia} {
		store.AddUser(v.User.ID, v.User.DisplayName, v.User.Emails[0].Email)
	}
	store.Grant(f.nia.User.ID, auth.RoleInstallationUser, auth.ScopeInstallation, uuid.Nil)
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, AcceptsJoinRequests: true, CreatedAt: now}
	store.Orgs[f.acme.ID], store.Slugs["acme"], store.Slugs["acme-corp"] = f.acme, f.acme.ID, f.acme.ID
	store.AddMember(f.acme.ID, f.olivia.User.ID, auth.RoleOwner)
	store.AddMember(f.acme.ID, f.max.User.ID, auth.RoleMember)
	f.invitation = orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, OrgSlug: "acme", OrgName: "Acme", Email: "nia@example.com", RoleID: auth.RoleMember,
		InvitedBy: f.olivia.User.ID, State: orgs.StatePending, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	store.Invites[f.invitation.ID] = f.invitation
	return f
}

// call serves one request to the API as viewer and returns the recorder.
func (f *orgFixture) call(viewer *auth.Authenticated, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	as(viewer, NewHandler("v1", nil, f.service)).ServeHTTP(rec, req)
	return rec
}

type orgCase struct {
	name       string
	viewer     func(f *orgFixture) *auth.Authenticated
	method     string
	path       func(f *orgFixture) string
	body       string
	wantStatus int
	wantCode   string
	check      func(t *testing.T, f *orgFixture, body []byte)
}

func runOrgCases(t *testing.T, cases []orgCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newOrgFixture(t)
			var viewer *auth.Authenticated
			if tt.viewer != nil {
				viewer = tt.viewer(f)
			}
			rec := f.call(viewer, tt.method, tt.path(f), tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode != "" {
				checkBody(t, rec, "", tt.wantCode)
			}
			if tt.check != nil {
				tt.check(t, f, rec.Body.Bytes())
			}
		})
	}
}

func olivia(f *orgFixture) *auth.Authenticated { return &f.olivia }
func maxim(f *orgFixture) *auth.Authenticated  { return &f.max }
func nia(f *orgFixture) *auth.Authenticated    { return &f.nia }
func fixed(p string) func(*orgFixture) string  { return func(*orgFixture) string { return p } }

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body %s is not JSON: %v", body, err)
	}
	return v
}

func TestOrganizations(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "me carries the installation permissions", viewer: nia, method: "GET", path: fixed("/v1/me"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if u := decode[User](t, body); len(u.Permissions) != 1 || u.Permissions[0] != "orgs.create" {
					t.Errorf("me = %+v", u)
				}
			}},
		{name: "my organizations as anonymous", method: "GET", path: fixed("/v1/me/organizations"), wantStatus: 401, wantCode: "auth.unauthenticated"},
		{name: "my organizations", viewer: maxim, method: "GET", path: fixed("/v1/me/organizations"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Organizations []Organization }](t, body)
				if len(got.Organizations) != 1 || got.Organizations[0].Slug != "acme" || got.Organizations[0].ViewerPermissions[0] != "agents.assign" {
					t.Errorf("organizations = %s", body)
				}
			}},
		{name: "create", viewer: nia, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"initech","name":"Initech"}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.Slug != "initech" || o.Providers != nil || len(o.ViewerPermissions) == 0 {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "create without the permission", viewer: maxim, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"initech","name":"Initech"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "create with a bad slug", viewer: nia, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"Initech","name":"Initech"}`, wantStatus: 400, wantCode: "slug.invalid"},
		{name: "create with a taken slug", viewer: nia, method: "POST", path: fixed("/v1/orgs"), body: `{"slug":"acme","name":"Acme"}`, wantStatus: 409, wantCode: "slug.taken"},
		{name: "create without a body", viewer: nia, method: "POST", path: fixed("/v1/orgs"), wantStatus: 400, wantCode: "request.invalid"},
		{name: "get by an old slug", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme-corp"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.Slug != "acme" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "get as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "update", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme"), body: `{"name":"Acme Inc","acceptsJoinRequests":false,"providers":["google"]}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.Name != "Acme Inc" || o.Slug != "acme" || o.AcceptsJoinRequests || o.Providers == nil || (*o.Providers)[0] != "google" {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "update clears the providers with an empty list", viewer: olivia, method: "PATCH", path: fixed("/v1/orgs/acme"), body: `{"providers":[]}`, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.Providers != nil {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "update as a Member", viewer: maxim, method: "PATCH", path: fixed("/v1/orgs/acme"), body: `{"name":"Mine"}`, wantStatus: 403, wantCode: "auth.forbidden"},
	})
}

func TestMembersAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "members", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/members"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Members []Member }](t, body)
				if len(got.Members) != 2 || len(got.Members[0].Roles) != 1 {
					t.Errorf("members = %s", body)
				}
			}},
		{name: "members as an outsider", viewer: nia, method: "GET", path: fixed("/v1/orgs/acme/members"), wantStatus: 404, wantCode: "organization.not_found"},
		{name: "remove a member", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.max.User.ID.String() }, wantStatus: 204},
		{name: "remove as a Member", viewer: maxim, method: "DELETE", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.olivia.User.ID.String() }, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "remove nobody", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string { return "/v1/orgs/acme/members/" + f.nia.User.ID.String() }, wantStatus: 404, wantCode: "member.not_found"},
		{name: "leave", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/leave"), wantStatus: 204},
		{name: "the last Owner leaves", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/leave"), wantStatus: 409, wantCode: "org.last_owner"},
	})
}

func TestInvitationsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "invite", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"New@Example.com"}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Invitation](t, body); i.Email != "new@example.com" || i.RoleId != auth.RoleMember || i.OrgSlug != "acme" {
					t.Errorf("invitation = %s", body)
				}
			}},
		{name: "invite an Owner as an Owner", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"new@example.com","roleId":"` + auth.RoleOwner.String() + `"}`, wantStatus: 201},
		{name: "invite as a Member", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"new@example.com"}`, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "invite a bad email", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"nobody"}`, wantStatus: 400, wantCode: "email.invalid"},
		{name: "invite twice", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"nia@example.com"}`, wantStatus: 409, wantCode: "invitation.exists"},
		{name: "invite a member", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/invitations"), body: `{"email":"max@example.com"}`, wantStatus: 409, wantCode: "member.exists"},
		{name: "list", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/invitations"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ Invitations []Invitation }](t, body); len(got.Invitations) != 1 {
					t.Errorf("invitations = %s", body)
				}
			}},
		{name: "list as a Member", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/invitations"), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "cancel", viewer: olivia, method: "DELETE", path: func(f *orgFixture) string { return "/v1/orgs/acme/invitations/" + f.invitation.ID.String() }, wantStatus: 204},
		{name: "cancel nothing", viewer: olivia, method: "DELETE", path: func(*orgFixture) string { return "/v1/orgs/acme/invitations/" + uuid.New().String() }, wantStatus: 404, wantCode: "invitation.not_found"},
		{name: "mine", viewer: nia, method: "GET", path: fixed("/v1/me/invitations"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				if got := decode[struct{ Invitations []Invitation }](t, body); len(got.Invitations) != 1 || got.Invitations[0].Id != f.invitation.ID {
					t.Errorf("invitations = %s", body)
				}
			}},
		{name: "accept", viewer: nia, method: "POST", path: func(f *orgFixture) string { return "/v1/me/invitations/" + f.invitation.ID.String() + "/accept" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if o := decode[Organization](t, body); o.Slug != "acme" || len(o.ViewerPermissions) == 0 {
					t.Errorf("organization = %s", body)
				}
			}},
		{name: "accept someone else's", viewer: maxim, method: "POST", path: func(f *orgFixture) string { return "/v1/me/invitations/" + f.invitation.ID.String() + "/accept" }, wantStatus: 404, wantCode: "invitation.not_found"},
		{name: "accept once the inviter left", viewer: nia, method: "POST", path: func(f *orgFixture) string {
			f.store.Grant(f.max.User.ID, auth.RoleOwner, auth.ScopeOrganization, f.acme.ID)
			_ = f.store.Leave(t.Context(), f.olivia.User.ID, f.acme.ID, f.olivia.User.ID, now)
			return "/v1/me/invitations/" + f.invitation.ID.String() + "/accept"
		}, wantStatus: 409, wantCode: "invitation.inviter_gone"},
		{name: "decline", viewer: nia, method: "POST", path: func(f *orgFixture) string { return "/v1/me/invitations/" + f.invitation.ID.String() + "/decline" }, wantStatus: 204},
	})
}

func TestJoinRequestsAPI(t *testing.T) {
	ask := func(f *orgFixture) uuid.UUID {
		req, err := f.service.RequestToJoin(t.Context(), f.nia.User, "acme", "hi")
		if err != nil {
			t.Fatal(err)
		}
		return req.ID
	}
	runOrgCases(t, []orgCase{
		{name: "ask", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/join-requests"), body: `{"note":"Let me in"}`, wantStatus: 201,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if r := decode[JoinRequest](t, body); r.Note != "Let me in" || r.UserName != "Nia" || r.OrgSlug != "acme" {
					t.Errorf("request = %s", body)
				}
			}},
		{name: "ask without a note", viewer: nia, method: "POST", path: fixed("/v1/orgs/acme/join-requests"), body: `{}`, wantStatus: 201},
		{name: "ask an organization that takes none", viewer: nia, method: "POST", path: func(f *orgFixture) string {
			f.acme.AcceptsJoinRequests = false
			f.store.Orgs[f.acme.ID] = f.acme
			return "/v1/orgs/acme/join-requests"
		}, body: `{}`, wantStatus: 404, wantCode: "organization.not_found"},
		{name: "ask as a member", viewer: maxim, method: "POST", path: fixed("/v1/orgs/acme/join-requests"), body: `{}`, wantStatus: 409, wantCode: "member.exists"},
		{name: "ask twice", viewer: nia, method: "POST", path: func(f *orgFixture) string { ask(f); return "/v1/orgs/acme/join-requests" }, body: `{}`, wantStatus: 409, wantCode: "join_request.exists"},
		{name: "mine", viewer: nia, method: "GET", path: func(f *orgFixture) string { ask(f); return "/v1/me/join-requests" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ JoinRequests []JoinRequest }](t, body); len(got.JoinRequests) != 1 {
					t.Errorf("requests = %s", body)
				}
			}},
		{name: "list", viewer: olivia, method: "GET", path: func(f *orgFixture) string { ask(f); return "/v1/orgs/acme/join-requests" }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[struct{ JoinRequests []JoinRequest }](t, body); len(got.JoinRequests) != 1 || got.JoinRequests[0].UserName != "Nia" {
					t.Errorf("requests = %s", body)
				}
			}},
		{name: "list as a Member", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/join-requests"), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "approve", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/join-requests/" + ask(f).String() + "/approve" }, wantStatus: 204,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				if m, err := f.store.Member(t.Context(), f.acme.ID, f.nia.User.ID); err != nil || m.Roles[0].ID != auth.RoleMember {
					t.Errorf("Nia = %+v, %v", m, err)
				}
			}},
		{name: "approve as an Owner", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/join-requests/" + ask(f).String() + "/approve" },
			body: `{"roleId":"` + auth.RoleOwner.String() + `"}`, wantStatus: 204},
		{name: "approve as a Member", viewer: maxim, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/join-requests/" + ask(f).String() + "/approve" }, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "approve nothing", viewer: olivia, method: "POST", path: func(*orgFixture) string { return "/v1/orgs/acme/join-requests/" + uuid.New().String() + "/approve" }, wantStatus: 404, wantCode: "join_request.not_found"},
		{name: "decline", viewer: olivia, method: "POST", path: func(f *orgFixture) string { return "/v1/orgs/acme/join-requests/" + ask(f).String() + "/decline" }, wantStatus: 204},
	})
}

func TestFail(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "validation", err: &orgs.ValidationError{Field: "slug", Code: "slug.invalid", Message: "bad"}, wantStatus: 400, wantCode: "slug.invalid"},
		{name: "forbidden", err: auth.ErrForbidden, wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "not found", err: orgs.ErrNotFound, wantStatus: 404, wantCode: "thing.not_found"},
		{name: "not a member", err: orgs.ErrNotMember, wantStatus: 404, wantCode: "member.not_found"},
		{name: "a slug taken", err: orgs.ErrSlugTaken, wantStatus: 409, wantCode: "slug.taken"},
		{name: "the last owner", err: orgs.ErrLastOwner, wantStatus: 409, wantCode: "org.last_owner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var refused *apiError
			if err := fail(tt.err, "thing"); !errors.As(err, &refused) || refused.status != tt.wantStatus || refused.code != tt.wantCode {
				t.Errorf("fail() = %v, want %d %s", err, tt.wantStatus, tt.wantCode)
			}
		})
	}
	if err := fail(errorString("boom"), "thing"); err == nil || err.Error() != "boom" {
		t.Errorf("fail() of another error = %v, want it as it is", err)
	}
	rec := httptest.NewRecorder()
	internalError(rec, httptest.NewRequest(http.MethodGet, "/v1/orgs/x", nil), refuse(418, "tea.pot", "short and stout"))
	if rec.Code != 418 {
		t.Errorf("a refusal's status = %d, want 418", rec.Code)
	}
	checkBody(t, rec, "", "tea.pot")
}
