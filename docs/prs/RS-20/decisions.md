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
