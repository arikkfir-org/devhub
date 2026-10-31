# Cobranch

Cobranch is an all-in-one, opinionated development platform for individuals and teams, with AI central to the development process from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change, so a developer or a team needs nothing else to ship software.

It is a product, not an installation: anyone installs it on their own Kubernetes cluster with one Helm chart and gets a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs.

Cobranch has two faces: the open-source product at cobranch.dev, which anyone downloads and runs as their own
installation, and cobranch.app, our own installation of it, a paid self-serve service in the way github.com and
gitlab.com are. This repository is the product; running cobranch.app is a later slice, and the product never assumes it.
Cobranch was called DevHub until its rename ([ENG-221](https://linear.app/arikkfir/issue/ENG-221)); version 0.0.1 was
published under that name.

Cobranch is a Kfirs project, created by Arik Kfir, and is released under the [MIT License](LICENSE).

## Status

Foundations. The Helm chart installs Cobranch's skeleton, whose servers answer only their version ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)), pipeline `main` publishes the images of `main`'s newest commit, and each GitHub release publishes the chart at its version ([ENG-207](https://linear.app/arikkfir/issue/ENG-207)). Sign-in through any OpenID Connect provider, sessions, organizations with their members, invitations and join requests, and their roles, built in and custom, with the grants of them, are in ([ENG-197](https://linear.app/arikkfir/issue/ENG-197), [the design](docs/cobranch/designs/sign-in-and-organizations.md)); API keys follow in the same issue. The work is tracked in Linear's [Cobranch project](https://linear.app/arikkfir/project/cobranch-56ee907dd1af), and the designs are cross-linked pages that start from the [product design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy); [`docs/cobranch/designs/`](docs/cobranch/designs/) holds snapshots of them.

## Layout

One repository holds every part of Cobranch, laid out as the [technical architecture](docs/cobranch/designs/architecture.md) agreed. Paths marked _planned_ arrive with the slices that need them.

| Path | What it holds |
| --- | --- |
| `cmd/cobranch/` | The one binary: a subcommand per workload (the API servers `auth-server`, `rest-server`, `graphql-server` and `admin-server`, the `migrate` Job, Git server, push worker, controller, `maintenance`, agent), so every workload runs the same build at one version; today `auth-server`, `rest-server`, `graphql-server`, `migrate`, `maintenance` and `version` |
| `internal/` | The Go packages: `services/` (the domain: `auth`, sign-in, sessions and the authorizer; `orgs`, organizations, members, invitations, join requests, roles and grants), `adapters/` (transports, storage, outside systems: `authserver`, `oidc`, `postgres`, `rest`, `graphql`, `server`, `blob`) and `system/` (configuration, telemetry, build information) |
| `web/console/` | The Console, which Caddy serves as static files (image `cobranch-console`): sign-in, the welcome page (create, join, invitations) and an organization's home, members, roles and settings under its slug, routed in the browser |
| `web/back-office/`, `web/ui/` | _Planned_: the Back-office and the components the web apps share |
| `api/` | The API contracts, from which `go generate ./...` writes the servers' stubs: REST (`openapi.yaml`), GraphQL (`graphql/`) and, with the Back-office, the admin API (`admin.yaml`) |
| `db/migrations/` | PostgreSQL's migrations, SQL and Go, embedded in the binary; `cobranch migrate` applies them before the servers roll out. Trigger `audit()` on the audited tables writes `audit_log` with the principal each transaction acts for |
| `db/queries/` | The stores' SQL, from which `go generate ./...` writes `internal/adapters/postgres/queries` with [sqlc](https://sqlc.dev) (`sqlc.yaml`), checked against the schema; no Go file writes a query but the schema version's read of goose's table |
| `db/seeds/` | Demo data, in Go over queries sqlc writes from its `queries.sql`, in every build; `cobranch migrate` adds it after the schema only with `COBRANCH_SEED` (the chart's `seed.enabled`), as a demo installation does |
| `sdk/` | _Planned_: the Go, Python and TypeScript SDKs |
| `chart/cobranch/` | The Helm chart that installs Cobranch |
| `Dockerfile` | Image `cobranch`: the binary, with `git` |
| `docs/cobranch/` | The design documents, published to the docs site under `cobranch/` |
| `.octomaton.yaml`, `.tekton/` | CI, run by [Octomaton](https://github.com/arikkfir-org/octomaton) |

## Development

Go 1.27, [golangci-lint](https://golangci-lint.run) 2.15 and a PostgreSQL 18 server for the tests that need one (each
makes its own database there; without `COBRANCH_TEST_DATABASE_URL` they are skipped):

```bash
docker run -d --name cobranch-postgres -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18.6-alpine
export COBRANCH_TEST_DATABASE_URL='postgres://postgres@localhost:5432/postgres?sslmode=disable'

go generate ./...                      # the REST and GraphQL stubs from api/, the queries from db/; CI fails if they differ
go vet ./... && go test -race ./...    # every test
golangci-lint run ./...                # the house rules' linters (.golangci.yml)
go build -o bin/ ./cmd/...             # bin/cobranch
bin/cobranch version
```

The workloads read `COBRANCH_` variables. `migrate` brings a database's schema to the build's version and exits; each
server needs a bucket and the database, and turns ready once the schema is at that version:

```bash
export COBRANCH_BLOB_URL='file:///tmp/cobranch-blobs?create_dir=true'
export COBRANCH_DATABASE_URL="$COBRANCH_TEST_DATABASE_URL"
bin/cobranch migrate
bin/cobranch rest-server                                 # :8080: /healthz, /readyz, /v1/version, /v1/me, /v1/orgs/…
COBRANCH_HTTP_ADDR=:8081 bin/cobranch graphql-server     # :8081: /healthz, /readyz, GraphQL at /
```

`auth-server` signs people in. It needs the Console's URL and its own, and an identity provider or the demo data
(`COBRANCH_SEED=true` with `migrate`, then with `auth-server`, lets the sign-in page sign in as a demo user);
`maintenance` deletes expired sign-in flows, sessions, invitations and join requests once and exits:

```bash
export COBRANCH_CONSOLE_URL=http://localhost:3000 COBRANCH_AUTH_URL=http://localhost:8082
export COBRANCH_AUTH_PROVIDERS='[{"id":"google","name":"Google","preset":"google","clientId":"…"}]'
export COBRANCH_AUTH_CLIENT_SECRET_GOOGLE='…'          # the redirect URI is $COBRANCH_AUTH_URL/callback
COBRANCH_HTTP_ADDR=:8082 bin/cobranch auth-server        # :8082: /providers, /sign-in, /callback, /sign-out
bin/cobranch maintenance
```

They log text here and JSON in a pod (`COBRANCH_LOG_FORMAT` picks one, `COBRANCH_LOG_LEVEL` the level), and send traces
wherever OpenTelemetry's own variables point, such as `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, or nowhere
([Observability](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/architecture.md#observability)).

CI runs the same on every pull request and merge queue group (check `Continuous Integration`), beside the organization's checks `Docs`, `Linear` and the AI review, pipeline `main` publishes both images of `main`'s newest commit, pipeline `tag` each version's images and chart, and pipeline `preview` each pull request's images, for a preview installation of its own, seeded with demo data (`db/seeds/`). How the repository and its CI fit together: [the Cobranch repository](docs/cobranch/designs/repository.md).

## Installing

Install a version of the chart, which is published beside its images; its guide,
[`chart/cobranch/README.md`](chart/cobranch/README.md), covers the values and upgrades:

```bash
helm install cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.3 \
  --namespace cobranch --create-namespace --set blob.url='gs://my-cobranch-bucket'
helm test cobranch --namespace cobranch
```

A version is a [GitHub release](https://github.com/arikkfir-org/cobranch/releases): publishing it creates tag `v1.2.3`,
whose pipeline `tag` adds `1.2.3` to the images pipeline `main` published for the commit and publishes the chart at
`1.2.3` ([Releases](docs/cobranch/designs/repository.md#releases)). Pipeline `main` publishes images `cobranch` and
`cobranch-console` of `main`'s newest commit to `me-west1-docker.pkg.dev/arikkfir/cobranch`, tagged with the commit's
first seven characters; an older commit may have none, since a push that a newer one overtakes while it waits is never
built. To follow `main`, install the chart from a checkout with its commit's images:

```bash
helm install cobranch chart/cobranch --namespace cobranch --create-namespace \
  --set-string images.cobranch.tag=$(git rev-parse HEAD | cut -c1-7) \
  --set-string images.console.tag=$(git rev-parse HEAD | cut -c1-7) --set blob.url='gs://my-cobranch-bucket'
```

For a commit of your own, build both images, push them where your cluster pulls from, and install with those
repositories and tag `dev`:

```bash
docker build --build-arg VERSION=dev -t registry.example.com/cobranch:dev .
docker build -t registry.example.com/cobranch-console:dev web/console
docker push registry.example.com/cobranch:dev && docker push registry.example.com/cobranch-console:dev
```

Every workload serves as if it were alone, so you choose where each one lives: a host each, or paths on one host
(`https://cobranch.example.com/api`), the prefix stripped. Route them from your Gateway, or set `urls.console`,
`urls.rest` and `urls.graphql` with `httpRoute.enabled` for an HTTPRoute each. Logs go to stdout as JSON; to see
traces, set `telemetry.traces.endpoint` to your OpenTelemetry Collector's or a vendor's OTLP endpoint.

## Contributing

Cobranch follows the organization's house rules (<https://docs.dev.kfirs.com/CONTRIBUTING.md>) and its own [`CLAUDE.md`](CLAUDE.md): a Linear issue for every change, pull requests through the merge queue, and a design document for every meaningful unit of work.
