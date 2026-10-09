# ADR 0001: vanilla rastrillo plus change records, scenarios and small contexts

Status: accepted. Carried into this project by rastrillo-starter, which
extracted it from gastos (ADR 0001 there, "a CARLOS playground testing
two conventions", where the claim was built and measured with drills).

## Decision

Build every app here on vanilla CARLOS/rastrillo plus a fixed set of
mechanical conventions, and nothing else from DDD in the code:

1. **Change records** (`libs/changes`). A `changes` table; every
   mutation on a tracked table lands a row in the same transaction or
   is refused before it reaches SQLite, by a GORM hook and, behind it,
   a TEMP trigger on every tracked table (raw SQL and `SkipHooks` do
   not get around it). `reason` is the event vocabulary in business
   language, past tense. `cause` chains changes (`change:12`) or names
   the root (`request:<trace id>`, `agent:<name>`); `request_id` is the
   chain's root on every link. `correlation_id` is the request's trace
   id, read off the write's context, never handed along. `actor` is
   `member:<id>`, `human` or `agent:<name>`. `entry_point` is the
   business name of what opened the write (ADR 0003). Append-only by
   trigger.
   `changes.Verify` (`<app> verify -db <copy>`) folds the record onto
   the live rows to find any row it cannot explain.
2. **Executable scenarios.** Given / When / Then in the customer's
   words, run through the real entry points; Then asserts visible
   state and the change records that landed, by reason.
3. **The binding test** holds the two together: every declared reason
   appears in a scenario's Then, and a Then cannot name an undeclared
   reason.
4. **Small bounded contexts.** One table, one vocabulary; identity
   shared by id only. A context is a reasons file that declares the
   tables it owns, a glossary (`CONTEXT-<context>.md`) and two tests:
   no change may carry one context's reason onto another's table
   (`noBleed`), and no query may join tables of two contexts (`make
   contexts`). A context moves another's data by causing a change
   there. Not a package split, not a context map.
5. **Telemetry is structural** (`libs/telemetry`). chi middleware and
   GORM callbacks give every request and statement a span and a
   duration sample; slog lines carry the trace id. The trace id IS the
   change record's correlation, so a log line, a span and a change of
   the same request share one handle. Export is off unless
   `OTEL_EXPORTER_OTLP_ENDPOINT` is set.

No kernel, bus, aggregates, event types, registry, DSL or generated
board. Event storming, impact mapping and specification by example stay
as customer practices; what survives in the code is the holding:
vocabulary becomes reason codes, examples become scenarios, one test
binds them.

Held in reserve, each with one trigger: a per-reason column allow-list
in the hook (the first time a scenario catches a wrong reason); a
Decider (`decide`/`evolve`, one file) for a flow whose `if` chains rot.

## What this gives and gives up

- "Why did this row change?" is `SELECT reason, cause FROM changes`,
  chained, or the `/changes` screen, without database access.
- Forward derivation by reason over history; unplanned reports can be
  backfilled from the record.
- The scenarios in the customer's words are the contract.
- Given up: enforcement that a *wrong* change cannot happen. The hook
  stops only an *unexplained* one; the allow-list in reserve recovers
  most of the rest if ever needed.

## Rules that come with it

- A mistake is corrected by a new change (`<Thing>Corrected`) whose
  cause is the change it corrects, with the row's fields written
  forward. Never by rewriting.
- Reason names are facts in the customer's words, past tense; never
  `Updated`, `Edited`, `Modified` (`TestReasonsAreFactsNotTimestamps`).
- Every table is in exactly one of `Tracked` and `Untracked`, and an
  untracked one says why (`TestEveryTableIsTrackedOrDeclaredNot`).
- A secret column is written in a second step and listed in
  `TrackedExempt`, so no diff carries it.
- Agents (a webhook, a schedule tick) open their own Trail with actor
  `agent:<name>`, as bearer-authenticated POSTs under `/agent/`.
- Work no request started opens its own trace with `telemetry.Start`,
  or its tracked writes are refused (`ErrUncorrelated`).
