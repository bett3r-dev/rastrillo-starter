# myproject

CARLOS/rastrillo apps in one repository, built on vanilla rastrillo
plus a fixed set of mechanical conventions and nothing else from DDD.
The why is `docs/decisions/0001-conventions.md`; the layout is
`docs/decisions/0002-monorepo-layout.md`; the flow map is
`docs/decisions/0003-flow-chain-extracted-not-enforced.md`; push to
the browser, when an app needs it, is
`docs/decisions/0004-real-time-sse-doorbell.md` (`libs/doorbell`);
tenancy (a process per tenant, socket-activated, exiting when idle) is
`docs/decisions/0005-tenancy-process-per-tenant.md`. Each app's own `AGENTS.md`
(`apps/<app>/AGENTS.md`) holds rastrillo's scaffold conventions and
that app's contexts.

## Layout

```
apps/<app>/      one rastrillo app per module, exactly as `rastrillo new` scaffolds it
libs/            shared code, one module: changes/ (the change record), telemetry/ (OTel),
                 activation/ (socket activation, idle exit), doorbell/ (SSE push)
clients/<name>/  shipped clients that are no app, one module each (none yet; ADR 0002)
tools/           not shipped: the prompt ledger
ops/             the local Grafana stack (observability/)
docs/decisions/  ADRs
```

- One rastrillo app needs one module root: rastrillo's dev loop and
  migration check refuse a second app under one `cmd/` or `internal/`.
  Never put a second program inside an app.
- An app requires `libs` through a `replace` in its own `go.mod`.
  `go.work` is a convenience for gopls and is never what makes a build
  work.
- An app never imports another app (Go's `internal/` already forbids
  it). Apps talk over HTTP, never through each other's databases.
- Shared code goes to `libs/` only once two apps need it (or an app
  and a client).
- A client (`clients/<name>`, ADR 0002's amendment) is its own module
  that ships but is no app: it never imports an app, talks to it over
  HTTP, requires `libs` by a `replace` only when it needs it, and has
  its own `make ci` with a contract test against the real app. The
  first one adds itself to the root gate.
- Inside an app, split by feature from its first large context (ADR
  0002): `internal/<app>` stays the registry (models, `BootSchema`,
  `Tracked`/`Untracked`, the aggregated reasons and contexts, the
  wiring of every policy between packages), the request glue features
  share is `internal/<app>/kit`, each feature is
  `internal/<app>/<feature>` with `Mount(r, Deps)`, and scenarios stay
  in `internal/<app>test`. Features never import each other or root
  (`make contexts`): a policy across packages is a function the
  reacting context exports, taken in the source's `Deps`, wired by
  root. blueprint2's `apps/server` ("Feature packages" in its
  AGENTS.md) is the worked example; `apps/myapp` is small enough to
  stay one package.

## The gate

`make ci` at the root: every app's own `make ci` (vet, gofmt,
staticcheck, govulncheck, budgets, tests, the perf lane, the migration
check, `make contexts`, `make flow-check`, and `make oracle` where the
app has one), then `libs` and `tools`, then gitleaks once.
`make -C apps/<app> ci` is one app's gate. While editing:
`go test -short ./internal/<app>/... ./internal/<app>test/` from the
app's directory. `CGO_ENABLED=0` throughout. Every Makefile pins the
same `GOTOOLCHAIN`; `make toolchain-check` holds them equal.

**The oracle** (`apps/<app>/oracle/`, build tag `oracle`, optional):
the scenarios again as black-box HTTP tests, one fresh process per
scenario on a free port, the Then read through `/changes.json`, so the
same suite judges another binary (`<APP>_ORACLE_BIN`) or a running
instance (`<APP>_ORACLE_URL`), and `<APP>_ORACLE_DUMP` keeps each
scenario's records. A test package, never a program (one module root
per app). It keeps its own copy of the vocabulary, held to
`reasons_*.go` by a test, and every reason must appear in one of its
Thens. `apps/myapp/oracle` is the pattern.

## The conventions, in every app

1. **Vanilla rastrillo.** Hand-written handlers, server-rendered HTML,
   integer cents. No kernel, no bus, no aggregates, no event types, no
   registry. One instance is one tenant: there is no tenant table, and
   members join by signing up on the instance. A tenant is one process,
   socket-activated, that exits when idle (ADR 0005). A product with
   central identity says so in its own AGENTS.md (blueprint2's ADR
   0005: an identity app signs people in, and an instance holds no
   credentials).

2. **Change records** (`libs/changes`). Every write on a table in the
   app's `Tracked` (models.go) must land a `changes` row in the same
   transaction or it is refused before it reaches SQLite. A handler
   opens a Trail (`a.trail(r, "WriteNote")`), says why with `Because(reason, cause)`,
   and writes with the Trail's context. `reason` is the event
   vocabulary in business language, declared in `reasons_<context>.go`.
   `cause` chains changes (`change:12`) or names the request. The
   table is append-only by trigger (`changes.Schema`, merged into the
   app's `BootSchema`). The correlation is the request's **trace id**,
   and nothing hands it along: the change hook reads it off each
   write's context (`Policy.Correlation`), stderr lines get it from
   `telemetry.Correlate`, Loki as `trace_id`, and the response sends it
   back as `X-Correlation-Id`. A tracked write outside any request is
   refused (`ErrUncorrelated`): work no request started opens its own
   trace with `telemetry.Start`.

   Each reason also declares **what it writes**: per table, the columns
   its change's diff may carry (`<context>Writes` beside the reasons in
   `reasons_<context>.go`, gathered in `ReasonWrites`, installed as
   `Policy.Allow`). A write whose table or column its reason does not
   declare is refused before SQLite (`ErrReasonMismatch`), naming the
   reason, the table and the column: the right context's reason on the
   wrong write (gastos's mutant M8b) passes every other check.
   `writes_test.go` holds the declaration to the vocabulary (every
   reason declares, only its own context's tables, only real columns).
   A create's diff is the whole row, so a reason that creates lists
   every column.

   **A change record is a state change that matters to the business**
   (the owner's ruling in blueprint2, 2026-10-10). The audit trail
   answers "what changed, and why", so a transient operation (a poll, a
   ring, a heartbeat, a claim that only takes work, a lease running on)
   is plumbing, never a change: it lands on an `Untracked` table, or
   nowhere. What such an operation finds that does change state lands
   as its own fact, under the report's entry point (a poll finds a
   merge: `BranchMerged`). Keep a state change you want to track what
   is going on with; drop an operation that merely happened. The cases
   blueprint2 settled (its `docs/porting.md`, "Transient or state"):
   plumbing are polls, a summon's ring, an agent's `working`
   heartbeat (kept nowhere), a notification's read mark and a tracker
   move (whose outcome is its own fact, caused by the fact that queued
   it); kept as facts are a real job's claim and loss, the lifecycle of
   a job that changes state outside (a tracker comment, a pull request
   readied), a role reaffirmed and elements pinned.

   A change can also carry a **scope**, the larger thing it belongs to
   (`kind:id`, `note:12`, `""` when unscoped), set where the Trail opens
   with `Within`, or `WithinCreated("note")` when the thing is the row
   the Trail's next write creates; every change the Trail records
   carries it, deletes included. `changes.InScope(ctx, db, scope,
   afterID)` reads that thing's changes since a cursor, in id order.
   `Policy.Admit` may refuse a write a scope does not take (a frozen
   session's), `Policy.Committed` is told the scopes each commit landed
   changes in (never a rollback: the doorbell's ring), and
   `Trail.OnRecord` writes a ledger beside each change in its
   transaction. A change made by someone acting as another (a platform
   admin as a member) keeps `actor` as who did it and says as whom in
   `on_behalf_of`; `/changes` shows both.

   Every change also carries its **entry point**: the business name
   of what opened the write, VerbNoun in the customer's words
   (`WriteNote`, `Join`; `BankReportedTransaction` for an agent), set
   inline where the Trail opens. Left unnamed, the hook records the
   method and route instead. `apps/<app>/docs/flow.md` and `flow.json`
   are the map read off the change records the scenarios land: each
   entry point's direct facts, the policies (a change caused by one in
   another context) and the chains
   `Command → [context] Event ⟹ [context] Event`. They are generated
   by `make flow` and never hand-edited; `make ci` fails only when
   they drift from what the scenarios produce. Nothing in them is a
   rule: unnamed entry points, mixed-context requests and implicit
   crossings are reported, not refused. They are the map of what the
   code does, and `flow.json` (versioned) is what a Blueprint board
   renders from.

   A cause counts as a parent only within one request (ADR 0003's
   same-request parent rule, adopted 2026-10-09). A change caused by a
   change from an earlier request is therefore a direct landing of its
   own entry point: a correction (`CorrectNote`), an agent fulfilling a
   request. Same-context follow-ups inside one request are still left
   out of the map. Change the extractor only when the map itself is
   wrong, and say so in the commit.

3. **Executable scenarios** (`internal/<app>test/*_scenario_test.go`).
   Given / When / Then in the customer's words, through the real entry
   points. Given joins members through `/signup` (where a product has
   central identity, Given signs them in through the real sign-in
   routes, with a test issuer playing the identity app, blueprint2's
   `signintest`); When is a form POST;
   Then asserts the screen (`sees`) **and** the change records that
   landed, by reason (`changed`). `scenario_test.go` is the
   vocabulary. Write the scenario before the handler.

**The binding test** (`binding_test.go`): every declared reason
appears in a scenario's Then, and a Then cannot name an undeclared
one. Adding a reason means adding a scenario. Deleting a scenario's
last use of a reason means deleting the reason.

**The route gate** (`coverage_test.go`): every route the router
mounts is reached by a `TestScenario*`, or is named in
`notScenarioRoutes` with a reason a reviewer can check. The hook, the
guard and `noBleed` fire only on a path that runs, so an unreached
route is a write path no convention has looked at. A stale exception
(a route no longer mounted, or one a scenario now reaches) fails too.
The forms and the way in and out are scenarios that land nothing
(`screens_scenario_test.go`).

4. **Bounded contexts, kept small.** One table, one vocabulary;
   identity shared by id only. A context is a reasons file
   (`reasons_<context>.go`, which declares the tables it owns), a
   glossary (`CONTEXT-<context>.md`, changed in the same commit as any
   change to a word's meaning there) and two tests: no change may carry
   a reason from one context on a table of another (`noBleed`, on every
   scenario's Then and on the fold), and no query may join tables of
   two contexts (`make contexts`). A context moves another's data by
   causing a change there. A context lives in exactly one package (a
   package may hold several, and features never import each other);
   not a context map. A new table gets its context's prefix.

5. **Telemetry is structural** (`libs/telemetry`: chi middleware plus
   GORM callbacks), so a new route or table needs nothing else. Every
   query takes the request's context (`a.db.G.WithContext(r.Context())`,
   or a helper's `ctx`), and every log line inside a request is a
   `*Context` call: that makes a statement's span a child of its
   request and puts the `trace_id` on the line. Export is off unless
   `OTEL_EXPORTER_OTLP_ENDPOINT` is set; `make -C apps/<app> observe`
   and `observe-run` are the local Grafana stack.

Held in reserve, not built: a Decider for a flow whose `if` chains
rot.

## Rules on top of rastrillo's

- A tracked write without a reason is a bug the hook catches, and raw
  SQL or `SkipHooks` does not get around it: a TEMP trigger on every
  tracked table refuses a write the hook did not declare first. The
  exemption is a secret column (`TrackedExempt`, e.g.
  `members.password_hash`), written in a second step so no diff
  carries it. Migrations run before the guard exists and another
  process's connection never sees it; `<app> verify -db <copy>` is the
  check after the fact.
- Reason names are facts in the customer's words, past tense
  (`NoteWritten`, `MemberJoined`). Never what the code did.
- Name every invariant, then give it a scenario.
- `/changes` is the audit trail as a screen. Keep it able to answer
  "why did this change?" without database access. `/changes.json?after=<id>`
  is the same rows for a machine, oldest first, 500 a page, behind
  the same sign-in; its field names (`id, reason, cause, request_id,
  correlation_id, actor, on_behalf_of, entry_point, scope, table,
  row_id, op, diff, created_at`) are a contract. A cause that names a
  change links to it, so a correction sits one click from what it
  corrects.
- A mistake is corrected by a new change, never by rewriting: a reason
  that says what was put right (`NoteCorrected`), with `cause` pointing
  at the change it corrects, and the row's fields written forward.
- Agents (a webhook, a schedule tick) open their own Trail with actor
  `agent:<name>`. They are bearer-authenticated POSTs under `/agent/`,
  outside the browser group (no CSRF evidence, no session).
- Push to the browser, when an app needs it, is SSE as a doorbell
  (ADR 0004, `libs/doorbell`): writes stay POSTs, an event only says
  something changed and carries the latest change id, and the client
  (`live.js`, vendored as `static/live.js`) re-fetches each `[data-live]`
  fragment. `Policy.Committed` rings the bell, so a rollback rings
  nothing; `apps/myapp`'s notes list is the example. A screen
  that cannot work without JavaScript is an island, named as the one
  exception in that app's AGENTS.md, with a read-only fallback.
- A durable inbox is a table, not rastrillo `jobs` (which is
  in-memory), and plumbing tables (an inbox, a tick ledger, a poll's
  queue) go in `Untracked` with the reason: what they did lands on
  tracked tables. A tracked table cannot hold a transient row (the
  guard refuses an undeclared write, and `verify` a row no change
  explains), so a transient kind of work gets its own untracked table.

## Adding an app

`make new-app NAME=<name>` scaffolds `apps/<name>` and wires it into
the repository (the libs replace, go.work). Then lay the conventions
over it, using `apps/myapp` as the reference, in this order, each step
with the app's gate green:

1. `migrations.go`: `BootSchema = migrate.Merge(sessions.Schema, changes.Schema, Schema)`.
2. `models.go`: `Tracked`, `Untracked` (each with its reason), `TrackedExempt`.
3. `reasons.go` (with `ReasonWrites`) plus one `reasons_<context>.go` (its reasons, tables and `<context>Writes`) and `CONTEXT-<context>.md` per context.
4. `handlers.go`: `trail(r, entryPoint)`, and an entry point named on every Trail the app opens.
5. `app.go`: `changes.Install` (with `Allow: ReasonWrites`), `telemetry.DB`, `telemetry.HTTP` first, the `correlation` middleware.
6. `cmd/<name>/main.go`: `telemetry.Setup`, the `Tee(Correlate(stderr), tel.Logs)` logger, `Shutdown` after `Serve`, the `verify` subcommand, and `exitWhenIdle` behind `<NAME>_IDLE_TIMEOUT` (`libs/activation`, off unset) with its test in `cmd/<name>/main_test.go`.
7. `internal/<name>test/`: `scenario_test.go` (with `collectFlow` in `given`), `harness_test.go` (`flowGate` after `budget.Main`), `binding_test.go`, `contexts_test.go`, `conventions_test.go`, `flow_test.go`, `writes_test.go`, `coverage_test.go` (`routeGate` after `flowGate`, the harness logger wrapped in `telemetry.Correlate`); the Makefile's `contexts`, `flow` and `flow-check` targets, the last two in `ci`; `make flow` once, and commit `docs/flow.md` and `flow.json`.
8. `make dev` and `observe` targets, the dashboard in `ops/observability/dashboards/`.

## The prompt ledger

What was asked, said, run and committed is kept in this repository's
own refs, in amadan's v1 format (`refs/amadan/ledger/<branch>`), so no
hosted service holds it. `tools/ledger` has the format; nothing here
needs amadan.

- Claude Code's Stop and SessionEnd hooks (`.claude/settings.json`)
  add each turn: prompts, replies, tool calls (input only, cut at 2,048
  characters) and token usage. The post-commit hook (`.githooks/`,
  wired by `make ledger-install`) marks each commit. Neither fails a
  turn or a commit; what goes wrong is in `.git/amadan-ledger/errors.log`.
- Land a branch with `go -C tools run ./cmd/ledger merge -m '<msg>' <branch>`,
  not `git merge`: a merge commit (`--no-ff`) that keeps the branch's
  commits and marks the merge in the ledger. A plain `git merge` loses
  the mark. Never squash (the owner's ruling, 2026-10-12): the
  branch's commits are the history.
- Read it with `go -C tools run ./cmd/ledger log [branch]`. Send it
  with `make ledger-push LEDGER_REMOTE=<remote>`, to every remote.
- Records are never rewritten or deleted, like `changes`. A tool call's
  input is recorded, so never put a secret on a command line.
