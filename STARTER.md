# rastrillo-starter

A starting point for a project of CARLOS/rastrillo apps, extracted from
gastos. `scripts/init` deletes this file in the project it makes.

```sh
scripts/init ~/Documents/development/<project> <project> <app>
cd ~/Documents/development/<project> && make ci && make ledger-install
git add -A && git commit -m 'chore: <project> from rastrillo-starter'
```

`init` copies what is committed here (`git archive HEAD`), renames
`myproject` to `<project>`, `myapp` to `<app>` and `MYAPP` to `<APP>`,
paths and contents, and runs `git init`. Both names must be lower-case
letters and digits.

## What a new project gets

On top of `rastrillo new -shell topbar <app>` (v0.28.0), the monorepo
layout of `docs/decisions/0002-monorepo-layout.md`:

- **libs/changes**: the change record. Trail, hook and TEMP guard
  triggers, cause chains that cannot loop, the diff by op, the entry
  point (named on the Trail, or the route when unnamed), `Verify`
  (the fold), and `Schema`, the append-only `changes` table as a
  subsystem's migration set; scope (`Within`, `WithinCreated`,
  `InScope`), `on_behalf_of`, `Policy.Admit`, `Policy.Committed` and
  `Trail.OnRecord`, as blueprint2 built them.
- **libs/telemetry**: OTLP push for traces, metrics and logs; chi
  middleware and GORM callbacks; the trace id as the correlation;
  slog `Tee` and `Correlate`; the SQLite file-size gauge; flush on
  SIGTERM.
- **libs/activation**: systemd socket activation and the idle exit
  (`<APP>_IDLE_TIMEOUT`), a tenant's scale to zero (ADR 0005).
- **libs/doorbell**: the SSE doorbell (ADR 0004): `Bell`, `Stream`,
  `Draining` and `live.js`, which an app vendors as `static/live.js`.
- **tools/ledger**: the prompt ledger in amadan's format, wired to
  Claude Code's Stop/SessionEnd hooks and a post-commit hook.
- **apps/<app>**: the example app wired to all of it, with two small
  contexts (accounts: members; notes: notes, with a correction) and
  one policy between them (a member joining writes a welcome note,
  caused by the join), a note's changes scoped to it, the notes list
  live through the doorbell, the idle exit, the flow map (`make flow` writes
  `docs/flow.md` and `flow.json`; `flow-check` is in `make ci`), the
  `/changes` screen, `<app> verify -db`, `make dev` with page reload,
  `make observe`, and the tests that hold the conventions: the
  scenario DSL, the binding test, `noBleed`, no joins across contexts,
  and the change-record conventions (append-only, request roots,
  trace correlation, raw SQL refused, every table placed, reasons as
  facts, chains descend, the fold).
- **ops/observability**: the grafana/otel-lgtm stack with the app's
  dashboard provisioned.
- **docs/decisions**: 0001 (the conventions), 0002 (the layout, with
  the open questions gastos's Teselly and Blueprint ADRs raised),
  0003 (the flow chain, extracted not enforced, with the same-request
  parent rule), 0004 (real-time as an SSE doorbell, accepted as built)
  and 0005 (tenancy: a process per tenant, socket-activated, exiting
  when idle).
- The root gate: every app's `make ci`, `libs` and `tools`, gitleaks
  once, the toolchain pins held equal.

## Keeping it current

The starter is a repository with its own gate: `make ci` here proves the
template is green before anyone copies it. A fix to `libs/` in a project
is a fix the starter wants too: carry it back by hand, since a project
made by `init` shares no history with this repository.
