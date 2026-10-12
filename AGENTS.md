# myproject

CARLOS/rastrillo apps in one repository, built on vanilla rastrillo
plus a fixed set of mechanical conventions and nothing else from DDD.
The why is `docs/decisions/0001-conventions.md`; the layout is
`docs/decisions/0002-monorepo-layout.md`; the flow map is
`docs/decisions/0003-flow-chain-extracted-not-enforced.md`; push to
the browser, when an app needs it, is
`docs/decisions/0004-real-time-sse-doorbell.md` (`libs/doorbell`). Each app's own `AGENTS.md`
(`apps/<app>/AGENTS.md`) holds rastrillo's scaffold conventions and
that app's contexts.

## Layout

```
apps/<app>/      one rastrillo app per module, exactly as `rastrillo new` scaffolds it
libs/            shared code, one module: changes/ (the change record), telemetry/ (OTel),
                 activation/ (socket activation, idle exit), doorbell/ (SSE push)
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
- Shared code goes to `libs/` only once two apps need it.

## The gate

`make ci` at the root: every app's own `make ci` (vet, gofmt,
staticcheck, govulncheck, budgets, tests, the perf lane, the migration
check, `make contexts`, `make flow-check`), then `libs` and `tools`, then gitleaks once.
`make -C apps/<app> ci` is one app's gate. While editing:
`go test -short ./internal/<app>/... ./internal/<app>test/` from the
app's directory. `CGO_ENABLED=0` throughout. Every Makefile pins the
same `GOTOOLCHAIN`; `make toolchain-check` holds them equal.

## The conventions, in every app

1. **Vanilla rastrillo.** Hand-written handlers, server-rendered HTML,
   integer cents. No kernel, no bus, no aggregates, no event types, no
   registry. One instance is one tenant: there is no tenant table, and
   members join by signing up on the instance.

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

   A correction (caused by the change it corrects) is neither a direct
   landing nor a policy, so its entry point (`CorrectNote`) does not
   appear in `flow.md`. That is known and accepted (ADR 0003), not a
   bug in the extractor: do not change `flow_test.go` to make it show.
   Change the extractor only when the map itself is wrong, and say so
   in the commit.

3. **Executable scenarios** (`internal/<app>test/*_scenario_test.go`).
   Given / When / Then in the customer's words, through the real entry
   points. Given joins members through `/signup`; When is a form POST;
   Then asserts the screen (`sees`) **and** the change records that
   landed, by reason (`changed`). `scenario_test.go` is the
   vocabulary. Write the scenario before the handler.

**The binding test** (`binding_test.go`): every declared reason
appears in a scenario's Then, and a Then cannot name an undeclared
one. Adding a reason means adding a scenario. Deleting a scenario's
last use of a reason means deleting the reason.

4. **Bounded contexts, kept small.** One table, one vocabulary;
   identity shared by id only. A context is a reasons file
   (`reasons_<context>.go`, which declares the tables it owns), a
   glossary (`CONTEXT-<context>.md`, changed in the same commit as any
   change to a word's meaning there) and two tests: no change may carry
   a reason from one context on a table of another (`noBleed`, on every
   scenario's Then and on the fold), and no query may join tables of
   two contexts (`make contexts`). A context moves another's data by
   causing a change there. Not a package split, not a context map. A
   new table gets its context's prefix.

5. **Telemetry is structural** (`libs/telemetry`: chi middleware plus
   GORM callbacks), so a new route or table needs nothing else. Every
   query takes the request's context (`a.db.G.WithContext(r.Context())`,
   or a helper's `ctx`), and every log line inside a request is a
   `*Context` call: that makes a statement's span a child of its
   request and puts the `trace_id` on the line. Export is off unless
   `OTEL_EXPORTER_OTLP_ENDPOINT` is set; `make -C apps/<app> observe`
   and `observe-run` are the local Grafana stack.

Held in reserve, not built: a per-reason allow-list of columns the
hook checks (turn on when a scenario catches a wrong reason), and a
Decider for a flow whose `if` chains rot.

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
  "why did this change?" without database access. A cause that names a
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
  in-memory), and plumbing tables (an inbox, a tick ledger) go in
  `Untracked` with the reason: what they did lands on tracked tables.

## Adding an app

`make new-app NAME=<name>` scaffolds `apps/<name>` and wires it into
the repository (the libs replace, go.work). Then lay the conventions
over it, using `apps/myapp` as the reference, in this order, each step
with the app's gate green:

1. `migrations.go`: `BootSchema = migrate.Merge(sessions.Schema, changes.Schema, Schema)`.
2. `models.go`: `Tracked`, `Untracked` (each with its reason), `TrackedExempt`.
3. `reasons.go` plus one `reasons_<context>.go` and `CONTEXT-<context>.md` per context.
4. `handlers.go`: `trail(r, entryPoint)`, and an entry point named on every Trail the app opens.
5. `app.go`: `changes.Install`, `telemetry.DB`, `telemetry.HTTP` first, the `correlation` middleware.
6. `cmd/<name>/main.go`: `telemetry.Setup`, the `Tee(Correlate(stderr), tel.Logs)` logger, `Shutdown` after `Serve`, the `verify` subcommand, and `exitWhenIdle` behind `<NAME>_IDLE_TIMEOUT` (`libs/activation`, off unset) with its test in `cmd/<name>/main_test.go`.
7. `internal/<name>test/`: `scenario_test.go` (with `collectFlow` in `given`), `harness_test.go` (`flowGate` after `budget.Main`), `binding_test.go`, `contexts_test.go`, `conventions_test.go`, `flow_test.go`; the Makefile's `contexts`, `flow` and `flow-check` targets, the last two in `ci`; `make flow` once, and commit `docs/flow.md` and `flow.json`.
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
- Land a branch with `go -C tools run ./cmd/ledger merge -squash -m '<msg>' <branch>`,
  not `git merge`: it carries the branch's prompts into the commit and
  marks the merge. A plain `git merge` loses both.
- Read it with `go -C tools run ./cmd/ledger log [branch]`. Send it
  with `make ledger-push LEDGER_REMOTE=<remote>`, to every remote.
- Records are never rewritten or deleted, like `changes`. A tool call's
  input is recorded, so never put a secret on a command line.
