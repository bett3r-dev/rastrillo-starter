# ADR 0002: one repository, one Go module per rastrillo app

Status: accepted. Carried into this project by rastrillo-starter,
which built steps 1 to 4 of the order of work below; it was written as
ADR 0003 in gastos, the app the starter was extracted from. Read it
before moving any directory, adding an app or adding a shared package.

## Context

A project here is several programs, not one. Typical next to the
first app:

- **an admin panel**: an internal view across every tenant. One
  instance of an app is one tenant with its own SQLite database, so
  this view reads from many instances, never one database;
- **other services** later, sharing code with both.

The wish is a TypeScript-style monorepo: several deployable services
plus shared packages, in one repository, built and gated together.

## The constraint that decides the layout

Go itself would take the simplest shape, one module with a binary per
`cmd/<name>/`. rastrillo v0.28.0 does not. Its tools treat the
directory they run in as the root of exactly one app:

| Tool | Reads | Refuses when |
| --- | --- | --- |
| `rastrillo migration check` / `generate` / `status` (`cmd/rastrillo/migration.go`, `appPackage`) | `<root>/go.mod` for the module path, `<root>/internal/` for the app | more than one directory under `internal/` not ending in `test` |
| `rastrillo dev` (`cmd/rastrillo/dev.go`, `findAppPkg`) | `<root>/cmd/` | more than one directory under `cmd/` |
| the scaffolded `gitleaks` target (Makefile) | the git repository | the app is not the repository root: it prints a notice and **scans nothing** |

Found on 2026-10-07 in gastos: putting the prompt ledger at
`internal/ledger` failed `make ci` with `expected exactly one app
package under ./internal, found [gastos ledger]`. That is why the
ledger lives under `tools/`.

So the single-module shape breaks `make dev` and the migration check
as soon as a second app exists. **One rastrillo app needs one module
root**, and that decides everything below.

## Decision

```
myproject/                        the repository
  go.work                         every module, for gopls and local work
  Makefile                        the root gate: each module's gate, plus gitleaks once
  .amadan/ci.d/                   the root gate's steps
  apps/
    myapp/                        the first app, exactly as rastrillo scaffolds it
      go.mod                      requires myproject/libs, with a replace directive
      cmd/myapp/  internal/myapp/  internal/myapptest/
      manifest/  .rastrillo/budgets.txt  Makefile
    admin/                        the cross-tenant panel, `rastrillo new admin`
      go.mod  cmd/admin/  internal/admin/  internal/admintest/  ...
  libs/                           shared code, one module, many packages
    go.mod
    changes/                      the Change type, the hook, Verify's fold
    telemetry/
    feed/                         (not built) the tenant feed's wire types and its client
  tools/                          not shipped: the prompt ledger, later others
    go.mod
    ledger/  cmd/ledger/
  docs/  ops/
```

1. **One module per rastrillo app** under `apps/<name>/`, each in the
   exact shape `rastrillo new` scaffolds, so `make dev`, `make ci`, the
   migration check and the perf lane run from the app's directory
   unchanged.
2. **Shared code is one module, `libs/`**, with a package per concern.
   It is split into more modules only when two packages need
   dependency versions that conflict.
3. **Apps depend on `libs` through a `replace` directive** in their own
   `go.mod`:
   ```
   require myproject/libs v0.0.0
   replace myproject/libs => ../../libs
   ```
   This, not `go.work`, is what makes an app build. Checked 2026-10-07
   on Go 1.27.1: with the replace in place an app builds with
   `GOWORK=off`. That is how `make release`, a container build or any
   runner without the workspace file will see it.
4. **`go.work` is committed, as a convenience only**: gopls and a
   build from the repository root see every module. Nothing may depend
   on it.
5. **The root gate loops over modules.** With a `go.work` root,
   `go test ./...` fails (`directory prefix . does not contain modules
   listed in go.work`, checked 2026-10-07). The root `make ci` runs
   `make -C apps/<name> ci` for each app and `go vet` / `go test` for
   `libs` and `tools`, then gitleaks once at the root, since an app's
   own gitleaks target skips itself below the root.

### Boundaries

- **An app never imports another app.** Go enforces this: code under
  `apps/myapp/internal/` cannot be imported from outside
  `apps/myapp/`. What two apps share goes to `libs/`.
- **Apps talk over HTTP, never through each other's databases.** This
  is the bounded-context rule (one table, one vocabulary; identity by
  id only) applied across processes.
- **The admin panel is an app, not another bounded context inside
  an app.** Its tables are projections of other apps' data, so they
  are derived, not tracked: like any plumbing table, they stay out of
  `Tracked`. Anything the admin itself decides, such as suspending a
  tenant, is its own tracked write with its own reasons file.

### How the admin panel reads every tenant

**Pull, with the `changes` table as the outbox.** The table is already
written in the same transaction as the data it explains, append-only
by trigger, and ordered by id. A broker such as Redis Streams would
add a second copy of the log and a relay that still has to read this
table with a cursor.

Not built by the starter; the design for when the admin arrives.

- **Feed:** each app instance serves `GET /agent/changes?after=<id>&limit=500`,
  bearer-authenticated under `/agent/` like any agent. It
  returns changes in id order.
- **Cursor:** the admin keeps a row per tenant,
  `(tenant, last_change_id)`. It upserts each change keyed by
  `(tenant, change_id)`, so receiving one twice does no harm.
  Resetting a cursor to 0 rebuilds that tenant's projection.
- **Tenant registry:** the admin owns the list of tenants (name →
  URL + token). An instance does not know its own name, and its row
  ids are only unique within it, so admin-side keys are
  `(tenant, entity, entity_id)`.
- **Snapshot plus tail:** migrations write rows without changes, and
  rows older than the change log have none. So the feed also has a
  snapshot endpoint returning current rows plus the highest change id
  at that moment, and the admin tails from that id. Without it, a
  projection rebuilt from 0 disagrees with the live rows.
- **Personal data:** diffs carry names and emails. The feed sends only
  the columns on a per-table allow-list it declares, never raw diffs.
  `password_hash` is already excluded (`TrackedExempt`).
- **Ordering:** an `id > N` cursor is safe only because SQLite allows
  one writer at a time, so ids become visible in order. On Postgres a
  lower sequence id can commit after a higher one, and an id cursor
  silently skips rows. Revisit the cursor if storage ever changes.
- **Two kinds of projection:** a mirror of rows (folding diffs, as
  `changes.Verify` already does), and counts by reason (for example
  `NoteWritten` per tenant per week). The second tests ADR 0001's
  claim that reason names on diffs are enough without event types.
- **Push stays in reserve.** Its trigger is needing updates within
  seconds, thousands of tenants, or several consumers. Even then, a
  tenant only notifies the admin ("changes past N") and the admin
  still pulls.

### Gate, budgets, deployment

- Each app keeps its own `make ci`, `.rastrillo/budgets.txt`, perf
  lane and migration check, run from its directory. Budgets are per
  directory, so a new app or `libs` package does not use up another's
  allowance.
- `govulncheck` and `staticcheck` run per module, since each has its
  own dependency graph. `gitleaks` runs once, at the root.
- Each app is one binary, one CARLOS unit and one database, released
  and deployed on its own (`make -C apps/<name> release`). `libs` and
  `tools` are never deployed.
- Each app pins its own `GOTOOLCHAIN` in its Makefile. Keep them
  equal; a root check comparing them is cheap.

## Order of work

Steps 1 to 4 (apps under `apps/`, `tools/` and `libs/` as their own
modules, the root `go.work`, Makefile and `.amadan/ci.d` with gitleaks
at the root) are what the starter ships. What remains:

5. **Add the tenant feed to the app**, starting with the scenario:
   Given a tenant, When the agent pulls after N, Then it sees the
   changes after N, allow-listed.
6. **Scaffold the admin** (`make new-app NAME=admin`), with the tenant
   registry, cursor and first projection.

## Rejected

- **One module, a binary per `cmd/<name>`.** It is the idiomatic Go
  shape and was the first suggestion, but rastrillo's dev loop and
  migration check refuse a second app (above). It becomes possible if
  rastrillo gains a way to name the app (say `-app admin`), which is
  worth asking upstream.
- **A repository per service.** Shared code would then need versioned
  releases, and a change that spans an app and the admin would become
  two PRs in a fixed order.
- **`go.work` as the only link between modules.** Builds that do not
  see the workspace file (`GOWORK=off`, a container, `make release`)
  could not resolve `libs`.

## Open questions

- **Module paths:** keep dotless paths (`myproject/libs`), or use a real
  domain? A domain matters only if something outside this repository
  will `go get` a module.
- **`libs` as one module or one per package:** one until two packages
  need dependency versions that conflict.
- **The first admin projection:** a mirror of rows, or counts by
  reason? The second is the cheaper test of ADR 0001.
- **Upstream:** would rastrillo accept a flag to pick the app, so one
  module could hold several apps?
- **Inside an app, split by feature before the budget forces it.**
  Raised by gastos ADR 0005 (porting Teselly, 23 subdomains) and ADR
  0006 (Blueprint, files of 900 to 1,600 lines): an app with many
  contexts passes 5,000 lines per directory early. The likely answer
  is rastrillo's own path, `internal/<app>/<feature>` with
  `Mount(r chi.Router, deps)` called from `Router`, one feature
  package per context with its reasons file and glossary. Decide it
  before the first large context lands, not after the third; the
  `make contexts` check and the binding test then read every feature
  package, not just `internal/<app>/`.
- **Tenancy, per app.** One instance is one tenant (gastos: one
  household). What a tenant is for each new app (a team, an org, a
  customer) is that app's first decision.
