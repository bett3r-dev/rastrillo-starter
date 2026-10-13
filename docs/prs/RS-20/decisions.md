# RS-20 — decisions after the design

## D1 — Install's refusal of a shape its reason does not write is ErrBadInvariant, and it fires even when Allow is nil
kind: silent-seam
step: build · slice: 4 · decidedBy: executor
sources: [code:shapesWritten (libs/changes/changes.go), code:fits (libs/changes/changes.go), design:Starter: libs/changes]
rejected: skip the check when Allow is nil, as fits does — "a shaped table must be one the reason writes" cannot hold when nothing declares what it writes
supersedes: —
The design says only "Install refuses otherwise". The name follows the package's Err* style and the error names every bad shape, sorted. With Allow nil, any invariant with Shapes fails Install; every app installs Allow: ReasonWrites, so this costs nothing and keeps the rule from being silently off. The verifier accepted it.

## D2 — the rule check is never switched off: By with no Policy.Invariants is ErrRuleMismatch
kind: silent-seam
step: build · slice: 4 · decidedBy: executor
sources: [code:require (libs/changes/changes.go), design:Starter: libs/changes]
rejected: a nil Invariants disabling the check like a nil Allow — a check that can be switched off by omission is weaker than the design's "the named rule does not shape this (reason, table)"
supersedes: —
It follows directly from the design's wording. The check sits right after fits, and before scope, admit and correlation, so only the order of refusals changes. The verifier accepted it.

## D3 — Change.Rule is a string with default:null, not *string
kind: deviation
step: build · slice: 4 · decidedBy: executor
sources: [code:Change (libs/changes/changes.go), design:Starter: libs/changes, migration bullet]
rejected: *string — By("") is no different from naming no rule, so a pointer distinguishes nothing
supersedes: —
NULL in the table reads back as "". Slice 7's "rule omitted when null" in /changes.json is an omitempty on this field.

## D4 — two gaps left open in the invariant declaration: an empty Name, and two invariants shaping one (reason, table)
kind: shipped-finding
step: build · slice: 4 · decidedBy: verifier
sources: [code:shaping (libs/changes/changes.go)]
rejected: —
supersedes: —
An invariant with Name "" that shapes a (reason, table) makes that write impossible (By("") names nothing), and Install does not catch it. When two invariants shape one (reason, table), naming either is enough and only one is recorded. The design decides neither; Install does not yet check that names are unique or non-empty, and Guards and Context are carried but not enforced. Candidates for a later slice (6 adds the first real invariants).

## D5 — graph.json already carries capital-case labels, meta.app and meta.runtimeName; EdgeKey is not ported
kind: deviation
step: build · slice: 1 · decidedBy: executor
sources: [code:NodeKey (tools/cmd/blueprint/slug.go), code:EdgeKey (blueprint-go apps/server/internal/server/design/vocabulary.go), code:edge key (blueprint-go design/view.go:73-75), design:Starter: the graph]
rejected: leave labels and meta to slice 2 — its golden would be rewritten instead of extended; port EdgeKey — graph.json edges are {from, to, kind} and blueprint-go builds the key itself
supersedes: —
TestKeysAreTheOldIds is copied without its EdgeKey assertion, and the test says so. The capital case is built from Slugify's words, so it can never move an id, but it is not pinned against pv3's change-case on an acronym (HTTPServer → Http Server is assumed). The copy's comment cites vocabulary.go:30-37 and :107-131, the lines actually copied (diffed identical); the slice's :116-129 and design.md's :110-122 are both imprecise.

## D6 — the extractor's input errors exit 1 by name, with no ddd default
kind: silent-seam
step: build · slice: 1 · decidedBy: executor
sources: [design:Starter: the graph, map.json fork RS-20-F5, map.json fork RS-20-F8]
rejected: default the framework to ddd — that default is blueprint-go's reader of a push without the field (F8), and this repository commits its framework
supersedes: —
A missing config, a config with no framework, and no flow.json under the root each exit 1, naming what is missing, as the version-1 refusal does. F5's "exit 1 only on violations and collisions" is about which diagnostics fail the run (slice 3), not about unreadable input; the verifier asked that slice 3 keep these at exit 1 so blueprint-check fails on them too.

## D7 — deferred from slice 1: atomic writes, cross-app collisions, meta.source, a misleading test comment
kind: shipped-finding
step: build · slice: 1 · decidedBy: verifier
sources: [code:run (tools/cmd/blueprint/main.go), code:runOver (tools/cmd/blueprint/main_test.go:13-14)]
rejected: —
supersedes: —
graph.json is written with plain os.WriteFile; slice 3 owns "both files are written atomically". Nodes with one id are merged on purpose within one app (an event landed by two entry points is one node), so slice 3's nameCollisions must tell a cross-app clash from the same node landed twice. meta.source is not emitted (slice 2). The app name comes from the directory, not yet cross-checked against v2's app field. [doc] main_test.go:13-14 says runOver runs into a fresh directory "as make blueprint does", which is false: make blueprint writes <root>/.blueprint and blueprint-check (slice 9) uses a temp directory. Slice 9 rewords it.

## D8 — verify's cutoff when 0005 has not run: no ledger row or no rule column holds nothing; a ledgerless database with the column holds everything
kind: silent-seam
step: build · slice: 5 · decidedBy: verifier
sources: [code:ruleCutoff (libs/changes/verify.go), code:migrate apply/adopt applied_at (rastrillo v0.28.0 migrate/apply.go:38-42,326-328, adopt.go:177-179), code:migrate ids (set.go:126,174), design:Starter: libs/changes verify bullet, design:risk 4, xp:RUS-83 D11]
rejected: a missing 0005 row as a zero cutoff, holding every record (round 0) — a tenant not visited since the deploy is still at 0004, `myapp verify -db` does not migrate, and every record there predates the rule column, so D11 says none is held
supersedes: —
The design named the cutoff (the 0005 ledger row's applied_at, now verified: RFC3339Nano UTC text) but not the database without that row. The executor first held every record (strict, like D2); the verifier found it fails verify on any pre-0005 tenant (risk 4) and the fix round made both "no 0005 row" and "no rule column" hold nothing. A ledgerless database with the column (only AutoMigrate builds it) still holds every record. A ledger present with no row but the column present is reachable only by hand-editing; it holds nothing. The comparison is on parsed times in Go because created_at and applied_at use different text formats; a record at exactly applied_at is not held.

## D9 — Verify's call sites stay nil in slice 5; wiring myapp.Invariants into both moves to slice 7
kind: deviation
step: build · slice: 5 · decidedBy: orchestrator
sources: [code:verify subcommand (apps/myapp/cmd/myapp/main.go:174-200), code:TestTheRecordFoldsToTheLiveData (apps/myapp/internal/myapptest/conventions_test.go:237), human]
rejected: wire them in slice 5 — myapp.Invariants does not exist until slice 6
supersedes: —
The plan listed neither call site in slices 6 or 7, yet from slice 7's .By(EveryNewMemberIsWelcomed) on, a nil-invariants Verify reports every welcome change as naming an undeclared rule. conventions_test.go would catch it; the verify subcommand would not (no test runs it). Slice 7's touches now carry cmd/myapp/main.go, cmd/myapp/main_test.go and conventions_test.go, with a test that runs the verify subcommand.

## D10 — not checked by verify: a record naming a declared rule that does not shape its (reason, table); a test name now broader than its cases
kind: shipped-finding
step: build · slice: 5 · decidedBy: verifier
sources: [code:Verify (libs/changes/verify.go), code:TestVerifyWithoutALedgerHoldsEveryRecord (libs/changes/changes_test.go:1133)]
rejected: —
supersedes: —
The design's verify bullet names two checks (an undeclared rule at any age; a shaped record naming none after the cutoff); a record naming a declared rule that does not shape it is refused at write time (ErrRuleMismatch) but not re-checked after the fact. [doc] TestVerifyWithoutALedgerHoldsEveryRecord's middle case (a ledger without the 0005 row) now holds nothing, so its name describes only its first case; a rename is owed.

## D11 — an agent's external system is one node per (agent, context), runtimeName without the agent: prefix, source its first entry point's
kind: silent-seam
step: build · slice: 2 · decidedBy: executor
sources: [code:builder.app (tools/cmd/blueprint/main.go), design:Starter: the graph, edges table, map.json RS-20-F6]
rejected: one node per actor across contexts — the id needs a context, and F3 already splits a command per context
supersedes: —
The design says "an external-system per agent:<name> actor" and is silent on context and source. The node sits in the context of the command it invokes (bank_ext_bank), so an agent invoking commands in two contexts draws two nodes; that case is untested. meta.runtimeName is `bank`, not `agent:bank`, so the id is the same from label or runtime name. Its meta.source is the first invoking entry point's in flow.json's entry_points order, pinned by TestAnAgentsSourceIsItsFirstEntryPoint.

## D12 — a node with no source in flow.json gets no meta.source; invariant sources are read from sources.invariants
kind: deviation
step: build · slice: 2 · decidedBy: executor
sources: [code:builder.node (tools/cmd/blueprint/main.go), design:meta.source on every node]
rejected: a placeholder source — blueprint-schema requires a real {file, line}
supersedes: —
"meta.source on every node" holds only for a complete flow.json; slice 8's census gives every name a source, so a gap means a malformed flow.json. Slice 3's behaviour does not yet diagnose it: hand-off owed to slice 3 or 8. meta.source is repository-relative (apps/<app>/ plus the app-relative path), as pv3's fixtures are. flow.json also carries invariants[].source, which the extractor does not read; slice 8 must produce both from the same scan.

## D13 — a rule a hop names that no invariant declares exits 1
kind: silent-seam
step: build · slice: 2 · decidedBy: executor
sources: [design:F5, decisions:D6]
rejected: a diagnostic only — F5 governs diagnostics over a readable map, and an undeclared rule is unreadable input (the hook refuses it, so only a stale or hand-edited flow.json gets here)
supersedes: —
In the spirit of D6, the extractor names the rule and says to run make flow. Slice 3 must keep it at exit 1.

## D14 — a command's guard edge is not checked against its context; noBleed does not cover invariants
kind: shipped-finding
step: build · slice: 2 · decidedBy: verifier
sources: [code:noBleed (apps/myapp/internal/myapptest/scenario_test.go:196-203), decisions:D4]
rejected: —
supersedes: —
The executor justified this by noBleed; the verifier found that false: noBleed compares a change's reason context with its table context and never reads invariants, and Install does not enforce an invariant's Context or Guards (D4). A mis-declared invariant would draw a guarded-by edge across subdomains. Owner: Install's check when slice 6 adds the first real invariants, or a slice 3 diagnostic. Policies are guarded by F11 only (the rules their hop names); an invariant shaping a reason only a policy lands draws no command edge.

## D15 — a Trail learns the rules through Policy.Begin and Policy.Logger; every Trail the app opens goes through the installed policy
kind: silent-seam
step: build · slice: 6 · decidedBy: executor
sources: [code:Policy.Begin (libs/changes/changes.go), code:trail (apps/myapp/internal/myapp/handlers.go:52), code:createMember (apps/myapp/internal/myapp/handlers.go:102)]
rejected: a package-level registry of rules — the conventions forbid a registry; plain changes.Begin with no rules — Refuse on it is always the undeclared-rule error
supersedes: —
The design gives Refuse a rule but not how a Trail knows the declared ones. The app keeps its installed policy and opens every Trail with a.policy.Begin; the verifier's round 1 found Join still on changes.Begin (fixed; that line has no behaviour to pin until Join gains a rule). Test fixtures (writes_test.go:64, doorbell_scenario_test.go:158) keep changes.Begin: they never refuse. An agent added later must open its Trail with policy.Begin if it refuses.

## D16 — "panics in tests" is testing.Testing(), captured per Trail at Begin; production returns Refused.Err and the handler renders 500
kind: silent-seam
step: build · slice: 6 · decidedBy: executor
sources: [code:Refuse (libs/changes/changes.go), code:Lenient, LoggerOf (libs/changes/export_test.go)]
rejected: a global strict flag — it would serialise the parallel libs tests
supersedes: —
libs/changes now imports testing in non-test code; the verifier checked the production binary links only testing.Testing and registers no test flags. The handler's a.fail branch on Refused.Err is reachable only outside go test, so no test covers it.

## D17 — the refused line's trace id is `correlation`, not `trace_id`: the design's sentence is wrong, the code is right
kind: deviation
step: build · slice: 6 · decidedBy: verifier
sources: [code:Correlate (libs/telemetry/slog.go:76), design:design.md:205, AGENTS.md convention 2]
rejected: renaming the stderr field to trace_id — trace_id is the Loki/OTLP name, and AGENTS.md and main.go already say Correlate adds correlation
supersedes: —
design.md:205 says telemetry.Correlate adds trace_id. It adds `correlation`, and the scenario asserts it equals X-Correlation-Id. design.md is left as committed; /verify-build may correct the sentence.

## D18 — the invariant binding reads myapp.Rule<Name> anywhere in a scenario file; an invariant's context is held by an app test, not by Install
kind: deviation
step: build · slice: 6 · decidedBy: executor
sources: [code:invariants_test.go (apps/myapp/internal/myapptest), code:binding_test.go, decisions:D4, decisions:D14]
rejected: parsing Then clauses — binding_test.go reads reasons the same regex way, and the two should agree; checking Context/Guards in Install — left for a later slice
supersedes: —
Rules are constants Rule<Name>, beside Reason<Name>. "Appears in a Then" is therefore "named in a scenario file" (a mention in a helper would also bind it). TestEveryInvariantBelongsToItsContext checks names non-empty and unique, a rule sentence, a declared context, and only that context's reasons and tables in Guards and Shapes, which answers D14 and D4's empty or repeated names at the app level. Owed in slice 7: AGENTS.md "Adding an app" lists <context>Invariants and invariants_test.go.
