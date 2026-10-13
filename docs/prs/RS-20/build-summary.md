---
work_item: RS-20
plugin: bett3r-ai-workflow@0.109.0+unknown
base: 530ed22b2d65a887da7c332ba13d6810c5573ecf
slices:
  - id: 1
    name: "TRACER BULLET — the extractor writes commands and events from a v2 flow.json, ids by a ported slugify"
    origin: plan
    mode: worktree
    modeReason: null
    commit: a240eff76216768383ce130b3f8fe8ab55a71364
    passed: true
    attempts: 1
    fixRounds: []
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D5, D6, D7]
  - id: 2
    name: "The full mapping: policies, invariants, tables, agents and every non-ddd edge"
    origin: plan
    mode: sequential
    modeReason: owner-brief-no-worktree
    commit: ec36e8b
    passed: true
    attempts: 2
    fixRounds:
      - { cause: oracle-wrong, executor: fresh }
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D11, D12, D13, D14]
  - id: 3
    name: "Diagnostics: the non-ddd grammar, collisions and orphans; exit 1 only on violations and collisions"
    origin: plan
    mode: sequential
    modeReason: owner-brief-no-worktree
    commit: 230a9da
    passed: true
    attempts: 2
    fixRounds:
      - { cause: doc-wording, executor: fresh }
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D19, D20, D21]
  - id: 4
    name: "A change carries the rule that shaped it; a shaped write without it, or with the wrong one, is refused"
    origin: plan
    mode: worktree
    modeReason: null
    commit: 08df84bd4d53dc3ab814ddd8a56b13a4c010cb2f
    passed: true
    attempts: 1
    fixRounds: []
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D1, D2, D3, D4]
  - id: 5
    name: "verify holds records to their rules, from the rule column's migration onward"
    origin: plan
    mode: worktree
    modeReason: null
    commit: a700ef33e32f4ce60bed78c843b64175d14763a2
    passed: true
    attempts: 2
    fixRounds: 
      - { cause: invariant, executor: continued }
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D8, D9, D10]
  - id: 6
    name: "An unchanged correction is refused by ACorrectionChangesSomething"
    origin: plan
    mode: sequential
    modeReason: owner-brief-no-worktree
    commit: 1a01ffb
    passed: true
    attempts: 2
    fixRounds:
      - { cause: invariant, executor: fresh }
    verifier: pass
    redBeforeGreen: true
    postDesignDecisions: [D15, D16, D17, D18]
  - id: 7
    name: "The welcome note carries EveryNewMemberIsWelcomed, and /changes shows the rule"
    origin: plan
    mode: null
    modeReason: null
    commit: null
    passed: false
    attempts: 0
    fixRounds: []
    verifier: null
    redBeforeGreen: null
    postDesignDecisions: []
  - id: 8
    name: "flow.json v2: rules, invariants, writes and sources"
    origin: plan
    mode: null
    modeReason: null
    commit: null
    passed: false
    attempts: 0
    fixRounds: []
    verifier: null
    redBeforeGreen: null
    postDesignDecisions: []
  - id: 9
    name: "make blueprint over the repository's committed flow maps, gated in make ci"
    origin: plan
    mode: null
    modeReason: null
    commit: null
    passed: false
    attempts: 0
    fixRounds: []
    verifier: null
    redBeforeGreen: null
    postDesignDecisions: []
  - id: 10
    name: "blueprint-go: the edge grammar by framework, held to the starter's graph by a contract fixture"
    origin: plan
    mode: null
    modeReason: null
    commit: null
    passed: false
    attempts: 0
    fixRounds: []
    verifier: null
    redBeforeGreen: null
    postDesignDecisions: []
  - id: 11
    name: "blueprint-go: a session takes its framework from its latest reality graph"
    origin: plan
    mode: null
    modeReason: null
    commit: null
    passed: false
    attempts: 0
    fixRounds: []
    verifier: null
    redBeforeGreen: null
    postDesignDecisions: []
---
## What shipped

First /build invocation, attended, verify depth full, executor gate scoped, worktree pool of 3 (two used). It stopped at the slice budget (3) with slices 1, 4 and 5 landed; 2, 3 and 6-11 remain `passes: false`, and 2 and 6 are ready for the next run.

- Slice 1, the tracer bullet: `tools/cmd/blueprint` writes graph.json from a v2 flow.json, its ids from a verbatim copy of blueprint-go's Slugify/NodeKey, held by blueprint-go's own cases. The verifier judged that the seam holds for slices 2, 3 and 9. The v2 fixture is hand-written; slice 8 makes the real one.
- Slice 4: `changes.Invariant`, `Policy.Invariants`, `(*Trail).By`, ErrRuleRequired/ErrRuleMismatch/ErrBadInvariant, and migration 0005 (`changes.rule`, nullable, still append-only).
- Slice 5: `changes.Verify` takes the invariants. Fix round 1 (`invariant`, continued) corrected the cutoff for a database that has not run 0005 (D8, design risk 4): round 0 would have failed verify on every tenant still at 0004.

For later slices: Verify's two myapp call sites still pass nil invariants, and slice 7 now carries the wiring plus a test that runs the verify subcommand (D9). Two [doc] rewordings are owed: the tools/cmd/blueprint main_test.go runOver comment (carried on slice 9, D7) and the TestVerifyWithoutALedgerHoldsEveryRecord name (D10). The pool worktrees' provisioner agents ran `worktree-pool reset` from inside the detached worktree and were refused (checkout-is-detached-no-task-branch); running the reset from the main checkout worked. Every myapp `make ci` in a pool worktree ran 12 targets with 2 SKIPs (gitleaks: not the repository root; browser: no Chromium), so those are SKIPs, not passes. Root `make ci` has not been run on the branch.

Second /build invocation, attended, in the primary checkout (the owner's brief: no new worktree, so every slice ran sequentially although `worktree-pool size` said pool=2; modeReason `owner-brief-no-worktree`), verify depth full, executor gate scoped, every executor and verifier on opus. It stopped at the slice budget (3) with slices 2, 6 and 3 committed; 7-11 remain, and 7 (after 6) is ready next. Fix rounds: oracle-wrong 1 (slice 2: the shapes clause of a command's guard was unpinned), invariant 1 (slice 6: Join's Trail bypassed Policy.Begin), doc-wording 1 (slice 3: two comments overclaimed; recheck: skipped (doc round), scope-check check 5 clean). All fresh, none continued.

- Slice 2: the full non-ddd mapping. Agent nodes are per (agent, context) (D11). A sourceless node gets no meta.source (D12; slice 8's census owns it, D21).
- Slice 6: `(*Trail).Refuse`, `Policy.Begin`/`Policy.Logger`, the first invariant ACorrectionChangesSomething, refusedBy, and invariants_test.go. libs/changes imports `testing` for testing.Testing (D16). The log field is `correlation`, not design.md:205's `trace_id` (D17, design sentence owed a fix). AGENTS.md "Adding an app" owes the invariants step, in slice 7 (D18).
- Slice 3: diagnostics.json, atomic writes, exit 1 only on violations and collisions. Open: refuse a framework other than non-ddd (D21).

Gates run: `make -C apps/myapp ci` showed 12 ran and 2 SKIPPED (gitleaks: not the repository root; browser: no Chromium), which is not a pass. `make shared` passed on libs and tools. Root `make ci` has still not run on the branch. Nothing is pushed: the owner's brief said not to push.
