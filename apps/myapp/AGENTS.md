# myapp

The first app of this repository. The project's conventions (change
records, scenarios, bounded contexts, telemetry) are in the root
`AGENTS.md` and apply here in full; this file adds the app's own
contexts and rastrillo's scaffold conventions.

## Contexts

- **accounts** (`reasons_accounts.go`, `CONTEXT-accounts.md`): `members`.
- **notes** (`reasons_notes.go`, `CONTEXT-notes.md`): `notes`. The
  example context: replace it with the first real one and keep the
  shape.

One cross-context policy: `MemberJoined[accounts] ⟹
WelcomeNoteWritten[notes]`, the welcome note written in the joining
transaction and caused by the join. `docs/flow.md` is the map
(`make flow`); entry points are `Join`, `WriteNote`, `CorrectNote`.

---

# rastrillo scaffold conventions

A [rastrillo](https://amadan.net/rastrillo/rastrillo) app. Read the
framework's own SKILL.md (repo root, or in the module cache) before
writing app code — it is the whole app story in ~15KB. The conventions
below are load-bearing; the framework enforces most of them
mechanically.

## Layout

- `internal/<app>/` — the app: `models.go` (plain GORM structs
  + Models), `migrations.go` + `migrations/` (Schema, BootSchema),
  `app.go` (migrate.Apply + chi router), `handlers.go`,
  `render.go` + `templates/`. This is SKILL.md's five-file shape.
- `manifest/` — the declarative path, optional and equal: TOML or .go
  Resource manifests, each generating a full screen set under `gen/`
  (see manifest/README.md for the mounting recipe). A hand-written
  action file at a generated path (carrying `//go:build rastrillo_actions`)
  takes that one screen over, by existence.
- `gen/` — generated when manifests are used; never edit. Regenerate
  with `rastrillo generate`.

## Rules the family holds hard

- Every query touching user-owned rows goes through `scope.Owned` —
  reads AND writes, transactions included. A row that isn't yours 404s,
  never 403s.
- Never bind a request body onto a GORM model: explicit
  `map[string]any` + `.Select` allowlist.
- Migrations apply at boot, once each, and are never re-run or reversed.
- Schema changes: edit models.go, then run `rastrillo migration generate`
  and read the SQL it writes — it may be a full table rebuild, not just
  an `ADD COLUMN`. Never edit a migration that has shipped.
  Renames are hand-written (`rastrillo migration new rename_x`) because
  a rename is indistinguishable from a drop plus an add.
- Money is integer cents. A float never touches a value a person will
  be held to.
- Screens work with JavaScript disabled; destructive actions get their
  own confirm-page URL.
- The gate is `make ci` (vet, gofmt, staticcheck, govulncheck, gitleaks, budgets, tests, the perf lane and the migration check), the same definition CI runs. Run it before every push. `CGO_ENABLED=0` throughout: the stack is cgo-free by design. The Makefile names the Go release the gate and `make release` use (`GOTOOLCHAIN`); raise it with each Go security release.
- The scaffold's two placeholder index tests were repointed at
  `/signin`, the public page (`index_test.go`), and `TestPerfScreens`
  signs in through `/signup` first, so it measures the pages rather
  than the redirect.

## Testing and budgets

- While editing: `go test -short ./internal/<app>/... ./internal/<app>test/`.
  Before pushing: `make ci`. If it is too slow to run before every
  push, a budget has already failed: fix that, never skip the gate.
- Budgets: 5,000 source and 8,000 test lines per directory (`make budget`),
  10s per test package (`make test`), 150ms to first byte per GET
  screen and 500ms cold boot (`make perf`). Timing fails only on CI.
  Exceptions go in `.rastrillo/budgets.txt`, each with a reason.
- Past 5,000 lines, split `internal/<app>` by feature:
  `internal/<app>/<feature>` with `Mount(r chi.Router, deps)`, called
  from `Router`.
- Every test package has `func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }`
  (or calls `budget.Main` inside its own), every test starts with
  `t.Parallel()` (perf tests excepted), and tests copy the
  `dbtest` template rather than migrating.
- A test whose inputs Go cannot see (it execs `go build`, reads files
  outside its package, or uses the network) gets its own make target
  with `-count=1`, or Go's test cache will replay a stale pass.

## Serving

`rastrillo.Resolve` + `Serve` speak the CARLOS activation contract
(socket/addr/db argv, LISTEN_FDS, $STATE_DIRECTORY, `sidecar run`) —
the golden deployment target, not a requirement: `./app -addr :8080`
serves anywhere. `GET /healthz` and `GET /api/version` are answered
by the framework, outside app middleware.

## UX conventions (seeded from profile: considered)

Accessible by construction, progressively enhanced, and never dependent on JavaScript to be usable.

These are rastrillo's own conventions, and the profile is named for what it does rather than after anyone else's work. For wider reading on interface quality, impeccable.style, the WAI-ARIA Authoring Practices and Inclusive Components are all worth your time — none of them is endorsed by, or authoritative for, this framework.

- [x] Icons — lucide, inline — `internal/<app>/icons`
- [ ] Screen flow — one job per screen — a link, then a page for that action alone, then a confirmation; never two ways to start work side by side
- [x] Fields — label, hint, help and error in one envelope, wired with aria-describedby — `ui/field, ui/field-select, ui/field-text`
- [x] Destructive actions — their own confirm-page URL, never a modal — `ui/confirm-form`
- [x] State — never colour alone — always a text label too — `ui/status-pill, ui/badge`
- [ ] Dates — relative, with the absolute date in a title attribute
- [ ] Errors — server-rendered beside the field, in plain language
- [x] Empty states — say what the screen is for, not just that it is empty — `ui/empty-state`

`[x]` is enforced by a vendored component: use it, do not hand-roll an
equivalent. `[ ]` is guidance to apply by hand. A convention absent from
this list is one rastrillo has no opinion about.

Edit any line. **This file is the source of truth, not the profile name.**
The profile only seeded this list at scaffold time; nothing re-reads it, so
changing a line here changes what the app does. A convention deleted from
this file is not a convention to reinstate.
