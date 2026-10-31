package orgs_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/orgs/orgstest"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// fixture is Acme, with Olivia its Owner, Max a Member, Gwen a Guest and Mia a Manager (a custom role that manages
// members without org.grants.manage); Nia belongs nowhere and may create organizations; Max owns Globex too.
type fixture struct {
	store                       *orgstest.Store
	service                     *orgs.Service
	acme, globex                orgs.Organization
	olivia, max, gwen, mia, nia orgs.Viewer
	invitation                  orgs.Invitation  // Acme's pending invitation to Nia, as Member
	stale                       orgs.Invitation  // Acme's expired invitation to Nia
	request                     orgs.JoinRequest // Nia's pending request to Acme
	ownerInvitation             orgs.Invitation  // Olivia's invitation to Nia as Owner
	managerInvitation           orgs.Invitation  // Mia's invitation to Nia as Owner, which the grant rules refuse
}

func user(name, email string) orgs.Viewer {
	return orgs.Viewer{Principal: auth.Principal{ID: uuid.New(), Kind: auth.KindUser, DisplayName: name}, Emails: []auth.Email{{Email: email, Verified: true}}}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := orgstest.NewStore()
	f := &fixture{store: s, service: orgs.NewService(s, auth.NewAuthorizer(s))}
	f.service.SetClock(func() time.Time { return now })
	f.olivia, f.max, f.gwen, f.mia, f.nia = user("Olivia", "olivia@example.com"), user("Max", "max@example.com"),
		user("Gwen", "gwen@example.com"), user("Mia", "mia@example.com"), user("Nia", "nia@example.com")
	for _, v := range []orgs.Viewer{f.olivia, f.max, f.gwen, f.mia, f.nia} {
		s.AddUser(v.ID, v.DisplayName, v.Emails[0].Email)
	}
	s.Grant(f.nia.ID, auth.RoleInstallationUser, auth.ScopeInstallation, uuid.Nil)
	f.acme = orgs.Organization{ID: uuid.New(), Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember, AcceptsJoinRequests: true, CreatedAt: now}
	f.globex = orgs.Organization{ID: uuid.New(), Slug: "globex", Name: "Globex", MembershipRoleID: auth.RoleGuest, AcceptsJoinRequests: true, CreatedAt: now}
	for _, o := range []orgs.Organization{f.acme, f.globex} {
		s.Orgs[o.ID], s.Slugs[o.Slug] = o, o.ID
	}
	s.Slugs["acme-corp"] = f.acme.ID // an old slug
	s.AddMember(f.acme.ID, f.olivia.ID, auth.RoleOwner)
	s.AddMember(f.acme.ID, f.max.ID, auth.RoleMember)
	s.AddMember(f.acme.ID, f.gwen.ID, auth.RoleGuest)
	s.AddMember(f.acme.ID, f.mia.ID, orgstest.Manager.ID)
	s.AddMember(f.globex.ID, f.max.ID, auth.RoleOwner)
	invite := func(by uuid.UUID, role uuid.UUID, expires time.Time) orgs.Invitation {
		inv := orgs.Invitation{ID: uuid.New(), OrgID: f.acme.ID, Email: "nia@example.com", RoleID: role, InvitedBy: by, State: orgs.StatePending, CreatedAt: now.Add(-time.Hour), ExpiresAt: expires}
		s.Invites[inv.ID] = inv
		return inv
	}
	f.invitation = invite(f.mia.ID, auth.RoleMember, now.Add(time.Hour))
	f.stale = invite(f.olivia.ID, auth.RoleMember, now.Add(-time.Minute))
	f.ownerInvitation = invite(f.olivia.ID, auth.RoleOwner, now.Add(time.Hour))
	f.managerInvitation = invite(f.mia.ID, auth.RoleOwner, now.Add(time.Hour))
	f.request = orgs.JoinRequest{ID: uuid.New(), OrgID: f.acme.ID, UserID: f.nia.ID, State: orgs.StatePending, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	s.Reqs[f.request.ID] = f.request
	s.Events = nil
	return f
}

// A case runs one operation and expects an error (or none) and the events it recorded.
type testCase struct {
	name       string
	run        func(ctx context.Context, f *fixture) error
	wantErr    error
	wantCode   string // a ValidationError's code
	wantEvents []string
}

// got gives an error the runner's context, as the house rules want of every error that crosses a package.
func got(err error) error {
	if err != nil {
		return fmt.Errorf("got: %w", err)
	}
	return nil
}

func runCases(t *testing.T, cases []testCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			err := tt.run(t.Context(), f)
			var verr *orgs.ValidationError
			switch {
			case tt.wantCode != "":
				if !errors.As(err, &verr) || verr.Code != tt.wantCode {
					t.Fatalf("error = %v, want validation code %s", err, tt.wantCode)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(f.store.Events, tt.wantEvents) {
				t.Errorf("events = %v, want %v", f.store.Events, tt.wantEvents)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	runCases(t, []testCase{
		{name: "a user with orgs.create", run: func(ctx context.Context, f *fixture) error {
			org, err := f.service.Create(ctx, f.nia, "initech", " Initech ")
			if err != nil {
				return got(err)
			}
			m, err := f.store.Member(ctx, org.ID, f.nia.ID)
			if err != nil || len(m.Roles) != 1 || m.Roles[0].ID != auth.RoleOwner || org.Name != "Initech" || org.MembershipRoleID != auth.RoleMember || !org.AcceptsJoinRequests {
				t.Errorf("Create() = %+v, member %+v (%v), want Nia its Owner with the defaults", org, m, err)
			}
			return nil
		}, wantEvents: []string{"org.created", "member.joined"}},
		{name: "a user without orgs.create", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.max, "initech", "Initech")
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "a bad slug", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.nia, "Initech!", "Initech")
			return got(err)
		}, wantCode: "slug.invalid"},
		{name: "a reserved slug", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.nia, "settings", "Initech")
			return got(err)
		}, wantCode: "slug.reserved"},
		{name: "an empty name", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.nia, "initech", "  ")
			return got(err)
		}, wantCode: "name.invalid"},
		{name: "a slug an organization had", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Create(ctx, f.nia, "acme-corp", "Acme")
			return got(err)
		}, wantErr: orgs.ErrSlugTaken},
	})
}

func TestGetAndUpdate(t *testing.T) {
	runCases(t, []testCase{
		{name: "a member reads by the current slug", run: func(ctx context.Context, f *fixture) error {
			org, err := f.service.Get(ctx, f.gwen, "acme")
			if err == nil && org.ID != f.acme.ID {
				t.Errorf("Get() = %+v, want Acme", org)
			}
			return got(err)
		}},
		{name: "an old slug finds the organization", run: func(ctx context.Context, f *fixture) error {
			org, err := f.service.Get(ctx, f.gwen, "acme-corp")
			if err == nil && org.Slug != "acme" {
				t.Errorf("Get() slug = %s, want acme", org.Slug)
			}
			return got(err)
		}},
		{name: "an outsider learns nothing", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Get(ctx, f.nia, "acme")
			return got(err)
		}, wantErr: orgs.ErrNotFound},
		{name: "the viewer's organizations", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.Organizations(ctx, f.max)
			if err == nil && (len(list) != 2 || list[0].Slug != "acme" || list[1].Slug != "globex") {
				t.Errorf("Organizations() = %+v, want Acme then Globex", list)
			}
			return got(err)
		}},
		{name: "an Owner changes the settings", run: func(ctx context.Context, f *fixture) error {
			org, err := f.service.Update(ctx, f.olivia, f.acme.ID, orgs.Settings{Slug: "acme-inc", Name: "Acme Inc", MembershipRoleID: auth.RoleGuest, Providers: []string{"google"}})
			if err != nil {
				return got(err)
			}
			again, _ := f.service.Get(ctx, f.gwen, "acme")
			if org.Slug != "acme-inc" || again.Slug != "acme-inc" || org.MembershipRoleID != auth.RoleGuest || org.AcceptsJoinRequests || !slices.Equal(org.Providers, []string{"google"}) {
				t.Errorf("Update() = %+v, old slug finds %+v", org, again)
			}
			return nil
		}, wantEvents: []string{"org.updated"}},
		{name: "a Member may not", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.max, f.acme.ID, orgs.Settings{Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleMember})
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "a slug another organization has", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.acme.ID, orgs.Settings{Slug: "globex", Name: "Acme", MembershipRoleID: auth.RoleMember})
			return got(err)
		}, wantErr: orgs.ErrSlugTaken},
		{name: "a membership role that isn't an organization's", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Update(ctx, f.olivia, f.acme.ID, orgs.Settings{Slug: "acme", Name: "Acme", MembershipRoleID: auth.RoleInstallationUser})
			return got(err)
		}, wantCode: "role.invalid"},
		{name: "the viewer's permissions", run: func(ctx context.Context, f *fixture) error {
			held, err := f.service.ViewerPermissions(ctx, f.mia, f.acme.ID)
			if err == nil && !slices.Equal(held, []string{"org.members.manage", "org.read"}) {
				t.Errorf("ViewerPermissions() = %v", held)
			}
			return got(err)
		}},
	})
}

func TestInvitations(t *testing.T) {
	runCases(t, []testCase{
		{name: "a Manager invites with the membership role", run: func(ctx context.Context, f *fixture) error {
			inv, err := f.service.Invite(ctx, f.mia, f.acme.ID, " New@Example.com ", uuid.Nil)
			if err == nil && (inv.Email != "new@example.com" || inv.RoleID != auth.RoleMember || inv.ExpiresAt != now.Add(orgs.InvitationLifetime) || inv.OrgSlug != "acme") {
				t.Errorf("Invite() = %+v", inv)
			}
			return got(err)
		}, wantEvents: []string{"invitation.made"}},
		{name: "an Owner invites an Owner", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "new@example.com", auth.RoleOwner)
			return got(err)
		}, wantEvents: []string{"invitation.made"}},
		{name: "a Manager may not invite an Owner", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.mia, f.acme.ID, "new@example.com", auth.RoleOwner)
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "a Member may not invite", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.max, f.acme.ID, "new@example.com", uuid.Nil)
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "not an email", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "nobody", uuid.Nil)
			return got(err)
		}, wantCode: "email.invalid"},
		{name: "a member's email", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "max@example.com", uuid.Nil)
			return got(err)
		}, wantErr: orgs.ErrAlreadyMember},
		{name: "an email already invited", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "nia@example.com", uuid.Nil)
			return got(err)
		}, wantErr: orgs.ErrInvited},
		{name: "a Manager lists them", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.Invitations(ctx, f.mia, f.acme.ID)
			if err == nil && len(list) != 3 {
				t.Errorf("Invitations() = %d, want the 3 that wait", len(list))
			}
			return got(err)
		}},
		{name: "a Guest may not list them", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.Invitations(ctx, f.gwen, f.acme.ID)
			return got(err)
		}, wantErr: auth.ErrForbidden},
		{name: "a Manager cancels one", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.CancelInvitation(ctx, f.mia, f.acme.ID, f.invitation.ID))
		}, wantEvents: []string{"invitation.cancelled"}},
		{name: "cancelling an expired one", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.CancelInvitation(ctx, f.mia, f.acme.ID, f.stale.ID))
		}, wantErr: orgs.ErrNotFound},
		{name: "cancelling through another organization", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.CancelInvitation(ctx, f.max, f.globex.ID, f.invitation.ID))
		}, wantErr: orgs.ErrNotFound},
	})
}

func TestExpiredRowsDontBlock(t *testing.T) {
	runCases(t, []testCase{
		{name: "an expired invitation gives way to a new one", run: func(ctx context.Context, f *fixture) error {
			for id, inv := range f.store.Invites {
				if id != f.stale.ID {
					delete(f.store.Invites, id)
				}
				_ = inv
			}
			_, err := f.service.Invite(ctx, f.olivia, f.acme.ID, "nia@example.com", uuid.Nil)
			if _, stale := f.store.Invites[f.stale.ID]; stale {
				t.Error("the expired invitation remains")
			}
			return got(err)
		}, wantEvents: []string{"invitation.made"}},
		{name: "an expired request gives way to a new one", run: func(ctx context.Context, f *fixture) error {
			stale := f.request
			stale.ExpiresAt = now.Add(-time.Minute)
			f.store.Reqs[stale.ID] = stale
			_, err := f.service.RequestToJoin(ctx, f.nia, "acme", "")
			if _, remains := f.store.Reqs[stale.ID]; remains {
				t.Error("the expired request remains")
			}
			return got(err)
		}, wantEvents: []string{"join_request.made"}},
	})
}

func TestAcceptingInvitations(t *testing.T) {
	runCases(t, []testCase{
		{name: "the invitee sees theirs", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.MyInvitations(ctx, f.nia)
			if err == nil && len(list) != 3 {
				t.Errorf("MyInvitations() = %d, want 3 (the expired one gone)", len(list))
			}
			return got(err)
		}},
		{name: "someone else sees none", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.MyInvitations(ctx, f.gwen)
			if len(list) != 0 {
				t.Errorf("MyInvitations() = %v, want none", list)
			}
			return got(err)
		}},
		{name: "the invitee accepts", run: func(ctx context.Context, f *fixture) error {
			org, err := f.service.AcceptInvitation(ctx, f.nia, f.invitation.ID)
			if err != nil {
				return got(err)
			}
			m, err := f.store.Member(ctx, f.acme.ID, f.nia.ID)
			if org.ID != f.acme.ID || err != nil || len(m.Roles) != 1 || m.Roles[0].ID != auth.RoleMember {
				t.Errorf("AcceptInvitation() = %+v, member %+v (%v)", org, m, err)
			}
			return nil
		}, wantEvents: []string{"invitation.accepted", "member.joined"}},
		{name: "an Owner's invitation to an Owner", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.AcceptInvitation(ctx, f.nia, f.ownerInvitation.ID)
			return got(err)
		}, wantEvents: []string{"invitation.accepted", "member.joined"}},
		{name: "an inviter who may not grant the role", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.AcceptInvitation(ctx, f.nia, f.managerInvitation.ID)
			return got(err)
		}, wantErr: orgs.ErrInviterGone},
		{name: "an inviter who lost the permission", run: func(ctx context.Context, f *fixture) error {
			if err := f.store.Leave(ctx, f.mia.ID, f.acme.ID, f.mia.ID, now); err != nil {
				return got(err)
			}
			f.store.Events = nil
			_, err := f.service.AcceptInvitation(ctx, f.nia, f.invitation.ID)
			return got(err)
		}, wantErr: orgs.ErrInviterGone},
		{name: "someone who doesn't hold the email", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.AcceptInvitation(ctx, f.gwen, f.invitation.ID)
			return got(err)
		}, wantErr: orgs.ErrNotFound},
		{name: "an expired invitation", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.AcceptInvitation(ctx, f.nia, f.stale.ID)
			return got(err)
		}, wantErr: orgs.ErrNotFound},
		{name: "the invitee declines", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.DeclineInvitation(ctx, f.nia, f.invitation.ID))
		}, wantEvents: []string{"invitation.declined"}},
		{name: "declining twice", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.DeclineInvitation(ctx, f.nia, f.invitation.ID); err != nil {
				return got(err)
			}
			return got(f.service.DeclineInvitation(ctx, f.nia, f.invitation.ID))
		}, wantErr: orgs.ErrNotFound, wantEvents: []string{"invitation.declined"}},
	})
}

func TestJoinRequests(t *testing.T) {
	runCases(t, []testCase{
		{name: "a user asks to join", run: func(ctx context.Context, f *fixture) error {
			req, err := f.service.RequestToJoin(ctx, f.gwen, "globex", " Let me in ")
			if err == nil && (req.OrgID != f.globex.ID || req.Note != "Let me in" || req.UserName != "Gwen" || req.ExpiresAt != now.Add(orgs.JoinRequestLifetime)) {
				t.Errorf("RequestToJoin() = %+v", req)
			}
			return got(err)
		}, wantEvents: []string{"join_request.made"}},
		{name: "an organization that takes none answers like none", run: func(ctx context.Context, f *fixture) error {
			f.globex.AcceptsJoinRequests = false
			f.store.Orgs[f.globex.ID] = f.globex
			_, err := f.service.RequestToJoin(ctx, f.gwen, "globex", "")
			return got(err)
		}, wantErr: orgs.ErrNotFound},
		{name: "a slug nobody has", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.RequestToJoin(ctx, f.gwen, "nowhere", "")
			return got(err)
		}, wantErr: orgs.ErrNotFound},
		{name: "a member asking", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.RequestToJoin(ctx, f.max, "acme", "")
			return got(err)
		}, wantErr: orgs.ErrAlreadyMember},
		{name: "asking twice", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.RequestToJoin(ctx, f.nia, "acme", "")
			return got(err)
		}, wantErr: orgs.ErrRequested},
		{name: "the requester sees theirs", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.MyJoinRequests(ctx, f.nia)
			if err == nil && (len(list) != 1 || list[0].ID != f.request.ID) {
				t.Errorf("MyJoinRequests() = %+v", list)
			}
			return got(err)
		}},
		{name: "a Manager lists them", run: func(ctx context.Context, f *fixture) error {
			list, err := f.service.JoinRequests(ctx, f.mia, f.acme.ID)
			if err == nil && len(list) != 1 {
				t.Errorf("JoinRequests() = %+v", list)
			}
			return got(err)
		}},
		{name: "a Member may not list them", run: func(ctx context.Context, f *fixture) error {
			_, err := f.service.JoinRequests(ctx, f.max, f.acme.ID)
			return got(err)
		}, wantErr: auth.ErrForbidden},
	})
}

func TestDecidingJoinRequests(t *testing.T) {
	runCases(t, []testCase{
		{name: "a Manager approves with the membership role", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.ApproveJoinRequest(ctx, f.mia, f.acme.ID, f.request.ID, uuid.Nil); err != nil {
				return got(err)
			}
			m, err := f.store.Member(ctx, f.acme.ID, f.nia.ID)
			if err != nil || len(m.Roles) != 1 || m.Roles[0].ID != auth.RoleMember {
				t.Errorf("member = %+v (%v), want Nia a Member", m, err)
			}
			return nil
		}, wantEvents: []string{"join_request.approved", "member.joined"}},
		{name: "an Owner approves as Owner", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.ApproveJoinRequest(ctx, f.olivia, f.acme.ID, f.request.ID, auth.RoleOwner))
		}, wantEvents: []string{"join_request.approved", "member.joined"}},
		{name: "a Manager may not approve as Owner", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.ApproveJoinRequest(ctx, f.mia, f.acme.ID, f.request.ID, auth.RoleOwner))
		}, wantErr: auth.ErrForbidden},
		{name: "approving through another organization", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.ApproveJoinRequest(ctx, f.max, f.globex.ID, f.request.ID, uuid.Nil))
		}, wantErr: orgs.ErrNotFound},
		{name: "a Manager declines", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.DeclineJoinRequest(ctx, f.mia, f.acme.ID, f.request.ID))
		}, wantEvents: []string{"join_request.declined"}},
	})
}

func TestRemovalClosesEveryWayBackIn(t *testing.T) {
	runCases(t, []testCase{
		{name: "removal closes every way back in", run: func(ctx context.Context, f *fixture) error {
			// Nia joins by her request while an invitation to her waits, is removed, and neither the invitation nor a
			// request made before her removal lets her back.
			if err := f.service.ApproveJoinRequest(ctx, f.olivia, f.acme.ID, f.request.ID, uuid.Nil); err != nil {
				return got(err)
			}
			if _, err := f.service.MyInvitations(ctx, f.nia); err != nil || len(f.store.Invites) != 0 {
				t.Errorf("after joining, %d invitations remain (%v), want none", len(f.store.Invites), err)
			}
			if err := f.service.RemoveMember(ctx, f.olivia, f.acme.ID, f.nia.ID); err != nil {
				return got(err)
			}
			if _, err := f.service.AcceptInvitation(ctx, f.nia, f.invitation.ID); !errors.Is(err, orgs.ErrNotFound) {
				t.Errorf("AcceptInvitation() after removal error = %v, want not found", err)
			}
			if err := f.service.ApproveJoinRequest(ctx, f.olivia, f.acme.ID, f.request.ID, uuid.Nil); !errors.Is(err, orgs.ErrNotFound) {
				t.Errorf("ApproveJoinRequest() after removal error = %v, want not found", err)
			}
			_, err := f.store.Member(ctx, f.acme.ID, f.nia.ID)
			return got(err)
		}, wantErr: orgs.ErrNotFound, wantEvents: []string{"join_request.approved", "member.joined", "member.removed"}},
	})
}

func TestLeavingAndRemoving(t *testing.T) {
	runCases(t, []testCase{
		{name: "a Member leaves", run: func(ctx context.Context, f *fixture) error {
			if err := f.service.Leave(ctx, f.max, f.acme.ID); err != nil {
				return got(err)
			}
			if held, _ := f.store.Permissions(ctx, f.max.ID, auth.ScopeOrganization, f.acme.ID); len(held) != 0 {
				t.Errorf("Max still holds %v at Acme", held)
			}
			return nil
		}, wantEvents: []string{"member.left"}},
		{name: "the last Owner may not leave", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Leave(ctx, f.olivia, f.acme.ID))
		}, wantErr: orgs.ErrLastOwner},
		{name: "an Owner leaves once another exists", run: func(ctx context.Context, f *fixture) error {
			f.store.Grant(f.max.ID, auth.RoleOwner, auth.ScopeOrganization, f.acme.ID)
			return got(f.service.Leave(ctx, f.olivia, f.acme.ID))
		}, wantEvents: []string{"member.left"}},
		{name: "an outsider leaves nothing", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.Leave(ctx, f.nia, f.acme.ID))
		}, wantErr: orgs.ErrNotFound},
		{name: "an Owner removes a Member", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.olivia, f.acme.ID, f.max.ID))
		}, wantEvents: []string{"member.removed"}},
		{name: "a Manager removes a Guest", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.mia, f.acme.ID, f.gwen.ID))
		}, wantEvents: []string{"member.removed"}},
		{name: "a Manager may not remove a Member", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.mia, f.acme.ID, f.max.ID))
		}, wantErr: auth.ErrForbidden},
		{name: "a Manager may not remove the Owner", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.mia, f.acme.ID, f.olivia.ID))
		}, wantErr: auth.ErrForbidden},
		{name: "a Member may not remove", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.max, f.acme.ID, f.gwen.ID))
		}, wantErr: auth.ErrForbidden},
		{name: "the last Owner can't be removed", run: func(ctx context.Context, f *fixture) error {
			f.store.AddMember(f.acme.ID, f.nia.ID, auth.RoleOwner)
			f.store.Leave(ctx, f.olivia.ID, f.acme.ID, f.nia.ID, now) //nolint:errcheck // set-up: Nia is an Owner, then not
			f.store.AddMember(f.acme.ID, f.nia.ID, auth.RoleOwner)
			f.store.Events = nil
			// Two Owners now: removing one is fine, removing the last is not.
			if err := f.service.RemoveMember(ctx, f.nia, f.acme.ID, f.olivia.ID); err != nil {
				return got(err)
			}
			return got(f.service.RemoveMember(ctx, f.nia, f.acme.ID, f.nia.ID))
		}, wantErr: orgs.ErrLastOwner, wantEvents: []string{"member.removed"}},
		{name: "removing oneself is leaving", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.max, f.acme.ID, f.max.ID))
		}, wantEvents: []string{"member.left"}},
		{name: "removing nobody", run: func(ctx context.Context, f *fixture) error {
			return got(f.service.RemoveMember(ctx, f.olivia, f.acme.ID, f.nia.ID))
		}, wantErr: orgs.ErrNotFound},
		{name: "what expired goes", run: func(ctx context.Context, f *fixture) error {
			f.service.SetClock(func() time.Time { return now.Add(2 * time.Hour) })
			invitations, requests, err := f.service.DeleteExpired(ctx)
			if err == nil && (invitations != 4 || requests != 1) {
				t.Errorf("DeleteExpired() = %d, %d, want 4 and 1", invitations, requests)
			}
			return got(err)
		}},
	})
}

func TestMembers(t *testing.T) {
	f := newFixture(t)
	members, err := f.service.Members(t.Context(), f.gwen, f.acme.ID)
	if err != nil || len(members) != 4 {
		t.Fatalf("Members() = %+v, %v", members, err)
	}
	if _, err := f.service.Members(t.Context(), f.nia, f.acme.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("an outsider's Members() error = %v, want not found", err)
	}
	roles, err := f.service.Roles(t.Context(), f.gwen, f.acme.ID)
	if err != nil || len(roles) != 4 || roles[0].Name != "Owner" {
		t.Errorf("Roles() = %+v, %v", roles, err)
	}
	if _, err := f.service.Roles(t.Context(), f.nia, f.acme.ID); !errors.Is(err, orgs.ErrNotFound) {
		t.Errorf("an outsider's Roles() error = %v, want not found", err)
	}
	held, err := f.service.Permissions(t.Context(), f.nia)
	if err != nil || !slices.Equal(held, []string{"orgs.create"}) {
		t.Errorf("Permissions() = %v, %v", held, err)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		check   func() error
		wantErr bool
	}{
		{name: "a slug", check: func() error { return orgs.ValidateSlug("acme-2") }},
		{name: "a one-letter slug", check: func() error { return orgs.ValidateSlug("a") }, wantErr: true},
		{name: "a double hyphen", check: func() error { return orgs.ValidateSlug("a--b") }, wantErr: true},
		{name: "uppercase", check: func() error { return orgs.ValidateSlug("Acme") }, wantErr: true},
		{name: "reserved", check: func() error { return orgs.ValidateSlug("sign-in") }, wantErr: true},
		{name: "an email", check: func() error { return orgs.ValidateEmail("a@b.example") }},
		{name: "no @", check: func() error { return orgs.ValidateEmail("ab.example") }, wantErr: true},
		{name: "an uppercase email", check: func() error { return orgs.ValidateEmail("A@b.example") }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.check(); (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}
