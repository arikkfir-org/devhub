# Cobranch sign-in and organizations

> **Snapshot.** This design lives in its [sign-in and organizations
> page](https://claude.ai/artifact/RYmKGqZNHvvTuWw3b2HR9R), linked with Cobranch's other designs; this file is generated
> from it, with its diagrams, as of 2026-10-30, and synced again once Cobranch's shape settles.

**Status**: Agreed 2026-10-30 · [ENG-197](https://linear.app/arikkfir/issue/ENG-197) · Builds on [principals and
permissions](principals-and-permissions.md) · Adds the Auth server to the [architecture](architecture.md#components) ·
Built by [ENG-197](https://linear.app/arikkfir/issue/ENG-197), in four pull requests

A person signs in through an identity provider the installation trusts, becomes a user, and lands in an organization
they created or joined. From there its managers invite people, grant roles and make API keys for scripts. This slice
gives Cobranch its first tables, its Auth server and the Console’s first pages behind a sign-in.

## Scope

How people sign in, sign up and work in organizations ([ENG-197](https://linear.app/arikkfir/issue/ENG-197)), the first
slice that gives Cobranch tables of its own. It builds on [principals and permissions](principals-and-permissions.md),
which decided who the principals are and how grants work, and adds the Auth server to the
[architecture](architecture.md#components). Cobranch was DevHub until
[ENG-221](https://linear.app/arikkfir/issue/ENG-221) renamed it.

**This slice builds sign-in, organizations, roles and API keys; every other credential and principal waits**

![This slice builds sign-in, organizations, roles and API keys; every other credential and principal waits](sign-in-and-organizations/scope.svg)

The tables, the authorizer and the events they write are built for every principal kind at once; later slices add rows,
not new models.

Choices beyond the principals design are collected under [Decisions](#decisions). The owner agreed them, and settled the
[three questions](#settled-questions) they raised, on 2026-10-30.

## Components

**The Auth server joins the workloads; it alone talks to identity providers**

![The Auth server joins the workloads; it alone talks to identity providers](sign-in-and-organizations/components.svg)

The browser signs in at the identity provider itself, so Cobranch never sees a password. The migrate Job, not drawn,
creates the tables before the servers roll out.

- **The Auth server** is the only workload that talks to identity providers and the only one that sets the session
  cookie. It serves no data. It joins the chart as a Deployment of two replicas with no Kubernetes token
  ([deployment](architecture.md#deployment)).
- **The GraphQL and REST servers** authenticate every request themselves, from the cookie or an API key, through the
  service layer's one authorizer ([how a check runs](principals-and-permissions.md#how-a-check-runs)).
- **The maintenance CronJob** arrives with its first task: deleting expired sign-in flows, sessions, invitations and
  join requests every hour. **Agreed.**
- **The migrate Job** creates this slice's tables, the built-in roles and Anonymous, in the first migrations after the
  baseline.

## Sign-in

**Sign-in is OpenID Connect’s code flow with PKCE, finished by whichever replica the callback reaches**

![Sign-in is OpenID Connect’s code flow with PKCE, finished by whichever replica the callback reaches](sign-in-and-organizations/sign-in.svg)

The return path must be a path of the Console’s own URL. A flow that is late, used, or opened in another browser fails,
and the page offers to start again.

- **A flow is a row.** **Agreed:** the state, nonce, PKCE verifier and return path wait in PostgreSQL for 10 minutes,
  and a cookie binds the row to the browser that began it. Any replica finishes a flow another began, no signing key is
  needed, and a code delivered to another browser fails, which stops login CSRF.
- **PKCE and a nonce** on every sign-in, so neither a stolen code nor a replayed ID token signs anyone in.
- **An identity is its provider's issuer and subject.** **Agreed:** OpenID Connect's own unique pair, never the email,
  so renaming a provider in the chart's values keeps its users.
- **The return path** is a path of the Console's own URL; anything else returns to the Console's home.

### Identity providers

- **Any OpenID Connect provider.** **Agreed:** the principals design said Google only in phase 1, but go-oidc treats
  every issuer alike, so supporting one means supporting all. Google becomes a preset that fills in its issuer;
  Keycloak, Okta, Entra ID and Dex need only theirs. GitHub's OAuth, which is not OpenID Connect, stays later.
- **The chart's values list them.** **Agreed:** each provider's ID, name, issuer and client ID, with its client secret
  in a Secret ([configuration](#configuration)). The first administrator signs in before any Back-office exists, and the
  providers stay in Git with the rest of the installation.
- **Organizations narrow them.** Each organization lists which providers its members may use, all of them by default; a
  user's grants there count only in a session from one of those
  ([principals](principals-and-permissions.md#sign-in-and-sign-up)).

### Sign-up

**A new identity becomes a user only if the sign-up policy lets it**

![A new identity becomes a user only if the sign-up policy lets it](sign-in-and-organizations/sign-up.svg)

A known identity skips all of this, except that `auth.admins` is checked at every sign-in: it grants Installation admin
to a listed email, and takes back the grant it gave once the email leaves the list.

- **The sign-up policy** is `anyone` (the default), `domains` (a verified email in a listed domain) or `invited` (a
  pending invitation to a verified email). A first administrator's email may always sign up.
- **One email, one user.** **Agreed:** a verified email that another user already holds refuses the sign-up, and the
  page names the provider that user signs in with. Cobranch never merges users; linking a second identity to a user
  comes later. Emails are rows of `principal_emails`, one per email, so a user holds several later (the owner's ask,
  2026-10-30) with no change to the tables: this slice adds the sign-up's one and looks users up through that table.
- **The first administrators.** **Agreed:** the chart's `auth.admins` lists emails; a sign-in with one of them,
  verified, grants Installation admin. The list is declarative: an email that leaves it loses the grant the list gave,
  at its next sign-in, and grants made in the Back-office are never touched.
- **Self-serve organizations.** While `organizations.selfServe` is on, the default, every new user is granted
  Installation user, which holds `orgs.create`. Turning it off stops new grants; the Back-office takes back old ones.

### Sessions

**The session cookie is host-only whenever every workload shares one host**

![The session cookie is host-only whenever every workload shares one host](sign-in-and-organizations/cookie.svg)

URLs with no common parent below a public suffix, such as `cobranch.example.com` and `api.example.org`, stop the Auth
server at start: the [architecture](architecture.md#routes) puts every host under one parent domain.

**A session lives until it idles, ages out or is revoked**

![A session lives until it idles, ages out or is revoked](sign-in-and-organizations/session-life.svg)

Idle, Expired and Revoked fail the same way: the next request with the cookie is `401`, and the Console sends the
browser to sign in.

- **Opaque and hashed.** **Agreed:** a session's token is 32 random bytes; PostgreSQL keeps only its SHA-256, so a copy
  of the database signs nobody in. Every sign-in makes a new row, so a planted session never survives a sign-in.
- **Revocable at once.** Sign-out, the user's own sessions page, and later the Back-office revoke a row; the next
  request fails. A suspended user's sessions fail the same way.
- **Lifetimes** are the principals design's defaults: 7 days unused, 30 days in all
  ([lifetimes](principals-and-permissions.md#lifetimes)).

### Authenticating a request

**Every request finds its principal by the hash of its credential, then asks the authorizer**

![Every request finds its principal by the hash of its credential, then asks the authorizer](sign-in-and-organizations/request.svg)

Nothing is cached between requests, so a revoked session, key or grant fails on the very next one.

- **No cache.** **Agreed** in the principals design: grants are read once per request, so a revoked session, key or
  grant stops working on the next one.
- **Cross-site requests.** **Agreed:** a mutation authenticated by the cookie must be JSON and, when it carries an
  `Origin`, come from the Console's. `SameSite=Lax` alone doesn't stop hosts of the same site, such as a sibling under
  the same parent domain. GraphQL takes no mutation over `GET`.
- **A bad credential is `401`.** Only a request with no credential at all runs as Anonymous.

## Organizations

**A new user belongs nowhere until they create, ask to join or accept an invitation**

![A new user belongs nowhere until they create, ask to join or accept an invitation](sign-in-and-organizations/onboarding.svg)

The welcome page offers all three at once: create, join by slug, and the invitations waiting for the user’s verified
emails. Without `orgs.create`, it offers only the other two.

### Creating one

- **A slug and a name.** The slug is 2 to 39 lowercase letters, digits and single hyphens, unique among organizations
  and their old slugs, and none of the Console's own paths (`settings`, `sign-in`, `new` and the rest). A rename keeps
  the old slug as a redirect ([data model](architecture.md#data-model)).
- **One transaction** creates the organization, makes its creator a member and its first Owner, and records both events.
- **Settings** in this slice: the name and slug, the membership role (Member by default), the identity providers its
  members may use, and whether it accepts join requests.

### Invitations

**An invitation names an email; only a user who holds it, verified, can accept**

![An invitation names an email; only a user who holds it, verified, can accept](sign-in-and-organizations/invite.svg)

A manager cancels a pending invitation; an invitee declines one. Either way, and at expiry, it closes.

- **No mail in this slice.** **Agreed:** an invitation names an email, and only a user who holds that email, verified,
  can accept it. The Console shows it to them at sign-in, and gives the inviter a link to send however they like; the
  link is no secret. Sending mail, and verifying emails by mail, come later.
- **The inviter is checked twice.** **Agreed:** at the invitation and again at its acceptance, so someone who lost
  `org.members.manage` meanwhile adds nobody.

### Join requests

**A join request waits for a member manager**

![A join request waits for a member manager](sign-in-and-organizations/join.svg)

**Agreed:** an organization is found by its slug only, with no directory to browse. It accepts join requests by default,
and one that turns them off answers like one that doesn't exist.

### Members

**Leaving and removing keep every user somewhere, and every organization an Owner**

![Leaving and removing keep every user somewhere, and every organization an Owner](sign-in-and-organizations/leave.svg)

API keys and invitations the member made stay, since the organization owns them. A removed user who belongs nowhere else
returns to No organization.

- **The membership role.** **Agreed:** inviting or approving someone with the organization's membership role takes only
  `org.members.manage`, since the organization chose that role for everyone who joins. Any other role also takes the
  [grant rules](#roles-and-grants).
- **Removing is revoking.** **Agreed:** removing a member takes every permission of each role they hold there, so only
  Owners remove Owners.
- **What stays.** API keys and invitations the member made stay, since the organization owns them. A removed user who
  belongs nowhere else returns to No organization.

## Roles and grants

**Nobody grants or revokes more than they hold**

![Nobody grants or revokes more than they hold](sign-in-and-organizations/grant-check.svg)

Who may hold a grant: at an organization or its repositories, its members, its own principals, and Anonymous where the
installation and the organization allow it ([scopes](principals-and-permissions.md#scopes-and-grants)). Defining or
editing a custom role passes the same first two checks for every permission it lists.

- **Granting and revoking** take `org.grants.manage` and every permission of the role, at that scope. The rule for
  revoking is **Agreed** here; the principals design stated it for granting only.
- **Custom roles** have a name, a description and any organization or repository permissions their definer holds.
  Editing one changes every grant of it on the next request, and takes the permissions it listed as well as those it
  lists, since dropping one revokes it from every holder; one still granted can't be deleted, nor one the organization
  gives everyone who joins. Deleting one cancels the invitations that offered it. A custom role belongs to one
  organization, which grants it beside the built-in roles; its name is unique among them.
- **What this slice checks.** `orgs.create`, `org.read`, `org.manage`, `org.members.manage`, `org.grants.manage` and
  `org.principals.manage`. The other permissions already exist in the catalog, and custom roles can hold them, but
  nothing checks them until their slices arrive.

## API keys

**An API key is a principal of the organization, with a secret shown once**

![An API key is a principal of the organization, with a secret shown once](sign-in-and-organizations/api-key.svg)

The GraphQL server takes the same header. An API key expires in a year by default, or never if its manager says so.

**Rotating a key overlaps two secrets, so scripts change over without failing**

![Rotating a key overlaps two secrets, so scripts change over without failing](sign-in-and-organizations/rotation.svg)

A key whose every secret has expired fails each call with `401` until a manager rotates it. Deleting the key, in either
state, deletes its principal, secrets and grants; its events keep its ID and name.

- **A secret's shape.** **Agreed:** a fixed prefix, 32 random bytes in base62 and a CRC32 checksum, so a scanner spots a
  leaked one and checks it offline, as GitHub's tokens do. PostgreSQL keeps its SHA-256 and its last four characters,
  which the Console shows.

| Prefix | Credential | Slice |
| --- | --- | --- |
| `cbs_` | A session, in the cookie | [ENG-197](https://linear.app/arikkfir/issue/ENG-197) |
| `cbk_` | An API key's secret | [ENG-197](https://linear.app/arikkfir/issue/ENG-197) |
| `cbp_` | A personal token | [ENG-198](https://linear.app/arikkfir/issue/ENG-198) |

- **Both APIs take them,** as `Authorization: Bearer`. The APIs answer CORS for the Console's origin only, so keys are
  for scripts and servers, not for other sites' pages.

## Operations

Sign-in and sign-out are the Auth server's. Every other operation is in both GraphQL and REST, over one service layer
([API](architecture.md#api-conventions)); the Console uses GraphQL. **Until its build-out,** the Console's first
pages call the REST API, as its sign-in page already did; they switch to GraphQL when the Console grows its own client.

| Operation | Takes |
| --- | --- |
| List and revoke one's own sessions | The user, for themselves |
| Create an organization | `orgs.create` |
| See an organization, its members, roles and API keys | `org.read` |
| Rename it, change its slug and settings | `org.manage` |
| Invite, see and cancel invitations; see, approve or decline requests | `org.members.manage`; another role than the membership role adds the grant rules |
| Remove a member | `org.members.manage` and every permission of their roles |
| Ask to join, accept or decline an invitation, leave | The user, for themselves |
| Grant or revoke a role | `org.grants.manage` and every permission of the role |
| Define, edit or delete a custom role | `org.grants.manage` and every permission it lists |
| Create, rotate or delete an API key | `org.principals.manage`; its roles add the grant rules |

Every change is an event in the [architecture](architecture.md#events-and-background-work)'s `events` table from this
slice on; reading them as the audit trail comes later. Beneath the events, every row an audited table gains, loses or
changes is a row of `audit_log`, written by trigger `audit()` with the principal the transaction acts for, which the
store names in a session variable local to the transaction (`cobranch.principal_id`); a row with no principal is
Cobranch's own doing, as a migration, a seed or maintenance is. An update keeps the whole row before and only the
columns that changed; a session's token hash and its mere use never reach the log.

## The Console

**The Console’s first pages**

![The Console’s first pages](sign-in-and-organizations/console-map.svg)

Organization pages live under the organization’s slug, such as `/acme/members`, which is why the Console’s own paths are
reserved slugs.

The [design system](design-system.md#screens) has drawn Sign in, the organization step of sign-up, the organization's
home and its members. The roles and settings pages followed them with the same components; the API keys and profile
pages are drawn there before the Console builds them.

## Configuration

| Value | Default | Read by |
| --- | --- | --- |
| `urls.auth` | None | The Auth server, for its redirects; the Console, to send people there; all of them, with the other URLs, to name the cookie. The provider's redirect URI is it plus `/callback` |
| `auth.providers` | None | The Auth server: each provider's `id`, `name`, `issuer` (or `preset: google`), `clientId` and `clientSecret` (a Secret's key); the Console, their IDs and names |
| `auth.signUp` | `anyone` | The Auth server: `anyone`, `domains` or `invited` |
| `auth.signUpDomains` | None | The Auth server, under `domains` |
| `auth.admins` | None | The Auth server: the first administrators' emails |
| `organizations.selfServe` | `true` | The Auth server, at sign-up |
| `authServer` | 2 replicas | The chart: resources and placement, like every workload's |

An installation with no provider starts, and its Console says that sign-in isn't set up yet. **Agreed:** the chart
passes each value as a `COBRANCH_` environment variable.

## Previews and tests

- **Previews sign in as seeded users.** **Agreed:** an identity provider registers every redirect URI exactly, and every
  preview has a host of its own. With seeding on, which a real installation never turns on, the sign-in page also lists
  the seeded users, an Owner, a Member, a Guest and one with no organization, and signs in as any of them, so a reviewer
  tries each role ([previews](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8#previews)).
- **Tests run a fake provider.** Go tests serve one from `httptest`; the chart's kind test in CI installs the chart with
  the demo data on and signs in end to end as a seeded user, so no provider runs beside the cluster.

## Data model

These tables fill in the principals design's [data model](principals-and-permissions.md#data-model) for this slice.

**This slice’s tables: who a user is, how they prove it, and where they belong**

![This slice’s tables: who a user is, how they prove it, and where they belong](sign-in-and-organizations/er.svg)

Every row an organization owns carries its `org_id`, and every query is scoped by it: a custom role's `org_id` is its
organization's, and a built-in role's is null. Built-in roles, Anonymous and the
installation’s scope are rows the first migrations write.

## Rollout

**Four pull requests in Cobranch**

![Four pull requests in Cobranch](sign-in-and-organizations/rollout.svg)

Nothing of an installation is in the product: an operator configures sign-in through the chart’s values alone, and
Cobranch’s own pull request previews sign in as seeded users.

- **Each pull request keeps `main` working**, with its Console pages and tests. An installation that has no provider yet
  shows the "not set up" page until its operator sets `urls.auth` and `auth.providers`.
- **An installation is the operator's**, configured through the chart alone: a client registered at their identity
  provider with `urls.auth` plus `/callback` as its redirect URI, its secret in a Secret of theirs, and the Auth
  server's route beside the other workloads'. Cobranch's own pull request previews sign in as seeded users.

## Risks

- **An identity provider down** stops new sign-ins; sessions already made keep working.
- **An account taken over at the provider** is the Cobranch user. Organizations narrow which providers count.
- **Verified emails are the provider's word.** The `domains` policy and invitations trust `email_verified`; an operator
  adds only providers that verify emails.
- **A parent-domain cookie** reaches every host under that parent. With one host, `__Host-` takes that away; with
  several, the operator keeps untrusted hosts off the parent.
- **Seeded sign-in on a real installation** would let anyone in as a seeded user. It exists only with seeding on, which
  the chart's notes already forbid there, and seeded users hold no installation grants.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Any OpenID Connect provider from this slice; Google is a preset | One code path for every issuer; self-hosted teams use Keycloak, Okta, Entra ID or Dex; tests use a fake one. Changes [ENG-196](https://linear.app/arikkfir/issue/ENG-196)'s "Google only in phase 1" | Agreed |
| Providers come from the chart's values, client secrets from Secrets | The first administrator signs in before the Back-office exists; they stay in Git | Agreed |
| An identity is its provider's issuer and subject | OpenID Connect's unique pair; a renamed provider keeps its users | Agreed |
| A sign-in flow is a PostgreSQL row, bound to its browser by a cookie; PKCE and a nonce on every sign-in | No signing key; any replica finishes it; login CSRF and stolen codes fail | Agreed |
| Sessions are opaque tokens stored hashed, a new row per sign-in | Revocation works on the next request; a database copy signs nobody in | Agreed |
| The session cookie is `__Host-` on one host, otherwise `__Secure-` for the URLs' nearest common parent | No sibling host can plant a session on a single host | Agreed |
| Cookie-authenticated mutations take JSON only, from the Console's origin | `SameSite` doesn't stop hosts of the same site | Agreed |
| Sign-up refuses a verified email another user holds; emails are rows, not a column, so a user holds several later | No duplicate users; linking identities and more emails come later without a migration | Agreed |
| `auth.admins` grants Installation admin at sign-in, and takes back only what it gave | Declarative like every value; the Back-office's grants stay its own | Agreed |
| No mail in this slice: an invitation is accepted only by a user holding its email, verified; the inviter sends the link | No mail server to set up yet; the link is no secret | Agreed |
| An invitation checks its inviter again at acceptance | Someone who lost the permission adds nobody | Agreed |
| Organizations are found by slug only and accept join requests by default | No directory to leak; an organization can turn requests off | Agreed |
| The membership role takes only `org.members.manage` to invite or approve with; any other role adds the grant rules | The organization chose that role for everyone who joins | Agreed |
| Revoking a role, and removing a member, take every permission of the role | Only Owners remove Owners | Agreed |
| Secrets carry a prefix (`cbs_`, `cbk_`, later `cbp_`) and a checksum | Scanners spot a leaked one and check it offline | Agreed |
| An API key holds at most two secrets; rotating adds one | No downtime while scripts change secrets ([principals](principals-and-permissions.md#api-keys-and-personal-tokens)) | Agreed |
| The maintenance CronJob arrives now, deleting expired rows | Flows and sessions grow with every sign-in | Agreed |
| Previews sign in as seeded users, with seeding on only | A provider can't register a redirect URI per preview; reviewers try every role | Agreed |
| Groups, personal tokens, SSH and signing keys, login-less users, mail, linked identities, and deleting accounts or organizations come later | Keeps this slice to its issue | Agreed |

## Settled questions

The owner took every recommendation on 2026-10-30:

1. **Any OpenID Connect provider now, or Google only?** Any, with Google as a preset. It costs nothing more, and an
   installation signs in through whatever provider its operator already runs.
2. **Previews sign in as seeded users?** Yes, rather than a fake provider deployed with every preview.
3. **Mail in this slice?** No: invitations work by verified email and a link, and mail comes with notifications.

Cobranch is a Kfirs project, created by Arik Kfir.
