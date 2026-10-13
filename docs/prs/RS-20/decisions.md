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
