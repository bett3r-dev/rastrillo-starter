# ADR 0005: a tenant is a process, socket-activated, that exits when idle

Status: accepted, 2026-10-12 (RS-6). It takes the shape blueprint2
decided and built (its ADR 0005, one instance per org, and ADR 0006,
tenants as systemd units with scale to zero) as the default for every
app cut from this starter. One process serving many databases is kept
as an option, taken only on a measurement.

## Context

Convention 1 already says one instance is one tenant: there is no
tenant table, and a handler never sees a tenant. What it left open is
how instances are run, and that decides what a tenant costs while
nobody is using it.

Two Go repositories built on this starter, and the Rust arm, answered
it differently:

- **blueprint2** runs one process per tenant as a systemd template
  unit, socket-activated, exiting when idle (`libs/activation`, its
  `SERVER_IDLE_TIMEOUT`). Measured on macOS: a median of 55 ms from
  exec to the first response through an inherited socket, about 50 MB
  RSS idle, a 35 MB binary. "One process serving many SQLite files"
  was rejected there.
- **rustrillo** (its ADR 0002) put a `TenantResolver` seam in
  front of the database: the default resolves the one configured
  database (one process per tenant); the alternative maps a host to a
  database file, opens pools lazily and closes idle tenants LRU, all in
  one process. It showed that a front router resolving the whole app
  per tenant leaves every handler untouched.

rastrillo v0.28.0's `Serve` already takes a systemd socket (fd 3 under
`LISTEN_FDS`), so the process side needs no upstream change.

## Decision

**A tenant is one process over its own SQLite file**, started by its
socket and stopped when idle:

- A systemd template unit per app (`<app>@.socket` owning
  `/run/<app>/%i.sock`, `<app>@.service` with its `StateDirectory`),
  as blueprint2's ADR 0006 lays out. The first connection starts the
  process and systemd holds it until the process accepts.
- The process exits after `<APP>_IDLE_TIMEOUT` with no request in
  flight (`libs/activation`'s `Idle`, wired in `cmd/<app>/main.go` as
  `exitWhenIdle`, off when unset). An open doorbell stream (ADR 0004)
  is a request in flight, so a page someone has open keeps its tenant
  up. The process sends itself the SIGTERM systemd would, so the stop
  is `Serve`'s own drain.
- Scheduled work wakes a sleeping tenant through the same socket: a
  timer per tenant POSTs to its `/agent/` route.
- Only concurrently active tenants pay memory. An idle tenant has
  exited, so the deployment economy a single binary would buy is
  mostly reclaimed: what is left is about 50 MB per tenant that is
  awake at the same moment.

Identity is the app's choice. The default is convention 1's: members
sign up on the instance. A product with central identity takes
blueprint2's ADR 0005: a sign-in app is the identity provider, and an
instance holds no credentials. Its scenarios' Given then signs in
through a test issuer instead of joining through `/signup`.

## The option held in reserve: one process, many databases

rustrillo's resolver is the way to serve many tenants from one process: a
router in front resolves the whole app per tenant (one `App` per
database, opened lazily, closed LRU), so handlers do not change.
Blueprint2's objection, that every handler would change, is not the
cost.

**The trigger is a measurement**: the peak memory of the tenants awake
at once, per box, over a representative week, against what one
process would hold for them. Take the option only when that number
says the process-per-tenant box cannot hold its active tenants.

**Its cost**, to weigh against that number, is what the unit gives
for free and one process does not:

- per-tenant uid isolation (`DynamicUser`), so one tenant's process
  cannot read another's database;
- per-tenant memory and CPU caps (`MemoryMax`, `CPUQuota`), so one
  tenant cannot starve the others;
- Litestream per unit, restoring and replicating one database with
  its process; in one process it becomes one replicator over many
  files, started and stopped with the resolver's pools.

## Consequences

- `libs/activation` is in this starter, and every app wires it the
  same way (AGENTS.md, "Adding an app", step 6). Local development
  leaves `<APP>_IDLE_TIMEOUT` unset and serves until stopped.
- Nothing in an app names a tenant. Telemetry's `tenant` attribute
  (`<APP>_TENANT`) is the one place the word appears, as before.
- An app on central identity changes convention 1's "members join by
  signing up" in its own AGENTS.md, naming blueprint2's ADR 0005.
- Postgres, one database per tenant, is a later decision (RS-2): it
  changes the data layer, not this shape.

## Rejected

- **A shared schema with a tenant column** (rustrillo's option A): the
  opposite of convention 1. Every query would carry the check, and
  one missed `WHERE` is a leak.
- **Kubernetes** (gastos's ADR 0006, as blueprint2's ADR 0006
  measured it): Argo CD reconciles in minutes, the API in seconds
  plus a component for scale to zero, and SQLite on local disk pins
  every tenant to one box anyway.
