# ADR 0003: the flow chain is extracted, not enforced

Status: accepted. Carried into this project by rastrillo-starter from
gastos, where it is ADR 0004 (accepted 2026-10-08), folded from a
conversation about making commands and policies first-class and the
60-run benchmark it led to. The raw material stays in gastos, under
`docs/experiments/flow-chain-benchmark/`; every number below is
computed from its `results.json` by `tables.py` there. The asks,
arms and reasons named below are gastos's (a household's expenses and
bills); the decision is this project's.

In this repository each app has its own map: `apps/<app>/docs/flow.md`
and `flow.json`, written by `make -C apps/<app> flow` and checked by
`flow-check` in that app's `make ci`.

## Decision

**Extract, don't enforce.** Every change carries an entry point (the
business name of what opened the write, set inline where the Trail
opens, or the method and route when unnamed). `make flow` reads the
change records the scenarios land and writes `docs/flow.md` and
`docs/flow.json`: the entry points, the cross-context policies, the
chains, and what looks off (unnamed and empty entry points,
mixed-context requests, implicit crossings). They are the map of how
the code actually works. The only gate is drift: `make flow-check`,
in `make ci`, fails when the committed files are not what the
scenarios produce. Nothing in them is a rule.

This is arm D of the benchmark below.

## The question

Should business operations (commands) and cross-context rules
(policies) be first-class in the code? Can the chain

    Command → [context] Event ⟹ policy ⟹ [context] Event

be enforced, and what would enforcing it cost? The owner's goal is
forcing functions: constraints on how the app evolves when agents
change it without supervision.

## How the discussion got here

- rastrillo is not CRUD-bound. The manifest path is optional, gastos
  has none, and its routes are already one per intent (`/expenses`,
  `/settlements`, `/disputes/{id}/resolve`).
- The gap was that intents had no checked name. A route is a
  command in all but name.
- First idea: a declared command → reasons map, enforced by the
  change hook.
- The owner reversed the source of truth: extract the map instead of
  declaring it.
- Policies already exist as data: a change whose cause names a change
  in another context. Two were there: BillMatched ⟹ ExpenseRecorded
  and DisputeResolved ⟹ BalanceAdjustedAfterDispute.
- Then a benchmark to price enforcement before adopting it.

## The experiment

### Arms

- **A**: main (39e4654), nothing added.
- **B**: R1–R3 enforced in the gate, explained in a skill
  (`.claude/skills/flow-chain`), entry point names as inline
  literals.
  - R1: every change carries a business entry point name.
  - R2: an entry point's direct changes are in one context, the same
    every time.
  - R3: no implicit crossings.
- **C**: B plus structure given upfront: a `cmd(...)` route wrapper,
  declared commands per context, declared policies, and a `p.does`
  scenario helper.
- **B-opt**: the same enforcement as B, cheaper to learn. The rule
  inline in AGENTS.md instead of a skill; the doc regenerates on any
  full package test run; every gate failure prints the fix;
  corrections are shown in the doc.
- **D**: extraction plus a drift check only. No rules, no skill;
  unnamed actions recorded by route.
- **B+hint** and **D+hint**: the hard ask plus a prompt hint to
  describe the change as the chain first. The hint text is in
  `asks.json` (`round3.js` appends it to the ask).

### Asks

- **cross**: a member leaves the household; their bill shares are
  re-split.
- **control**: correct an expense's amount. Stays in one context.
- **hard**: a bill paid outside the bank and recorded as an expense
  counts its month as paid. A reverse crossing inside an existing
  command, with a loop trap against BillMatched ⟹ ExpenseRecorded.

The texts, verbatim, are in `asks.json`; the scripts pass them in as
arguments.

### Each run, and how it was scored

An agent in a fresh worktree from the arm's base, given the ask
verbatim. Scored by:

- a hidden acceptance test on database rows;
- `make ci`;
- a flow collector: cross-context edges, implicit crossings, bleeds;
- diff size;
- cost from the transcripts: context tokens (input + cache read +
  cache creation, summed over turns), turns, minutes.

The scorer was validated against a reference implementation, which
passes, and against main, which fails on behaviour (it compiles; the
routes are missing).

## Results

"Arrow" is the number of runs that produced the expected policy:
MemberLeft[expenses] ⟹ a reconciliation fact on cross,
ExpenseRecorded[expenses] ⟹ a reconciliation fact on hard. Control
has none to produce. Round 1 recorded no bleed count (n/r). Cost is
median (range).

| Round | Ask | Arm | n | Hidden pass | ci pass | Arrow | Implicit crossings | Bleeds | Context tokens | Turns | Minutes |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | cross | A | 5 | 5 | 5 | 5 | 0 | n/r | 2.93M (2.56M–3.81M) | 30 (27–35) | 5.9 (5.4–5.9) |
| 1 | cross | B | 5 | 5 | 5 | 5 | 0 | n/r | 4.39M (3.50M–6.06M) | 41 (35–54) | 5.3 (5.2–7.0) |
| 1 | cross | C | 5 | 5 | 5 | 5 | 0 | n/r | 4.16M (3.32M–4.58M) | 39 (34–43) | 6.5 (4.6–6.7) |
| 1 | control | A | 5 | 5 | 5 | n/a | 0 | n/r | 2.14M (2.04M–2.90M) | 25 (24–30) | 4.4 (3.8–5.3) |
| 1 | control | B | 5 | 5 | 5 | n/a | 0 | n/r | 3.33M (2.72M–4.05M) | 34 (27–39) | 4.7 (4.0–5.8) |
| 1 | control | C | 5 | 5 | 5 | n/a | 0 | n/r | 3.49M (3.01M–5.22M) | 33 (31–48) | 5.7 (4.9–6.5) |
| 2 | hard | A | 5 | 5 | 5 | 5 | 0 | 0 | 1.79M (1.29M–2.39M) | 20 (17–27) | 4.1 (4.0–4.3) |
| 2 | hard | B | 5 | 5 | 5 | 5 | 0 | 0 | 1.57M (1.46M–2.27M) | 19 (18–26) | 3.9 (3.3–5.6) |
| 2 | hard | B-opt | 5 | 5 | 5 | 5 | 0 | 0 | 1.51M (1.25M–1.69M) | 20 (16–22) | 5.4 (4.2–5.5) |
| 2 | hard | D | 5 | 5 | 5 | 5 | 0 | 0 | 1.54M (1.47M–1.80M) | 19 (18–22) | 4.3 (3.3–5.8) |
| 3 | hard | B+hint | 5 | 5 | 5 | 5 | 0 | 0 | 2.17M (1.61M–2.56M) | 24 (17–29) | 8.7 (6.4–8.8) |
| 3 | hard | D+hint | 5 | 5 | 5 | 5 | 0 | 0 | 1.80M (1.46M–2.42M) | 19 (18–26) | 8.7 (6.8–9.5) |

- **Round 1** (cross and control, A/B/C): all 30 passed. All 15
  cross runs produced MemberLeft[expenses] ⟹ reconciliation. Zero
  implicit crossings. On context tokens B cost +50% (cross) and +56%
  (control) over A; C cost +42% and +64%.
- **Round 2** (hard, A/B/B-opt/D): all 20 passed, all produced
  ExpenseRecorded[expenses] ⟹ BillPaidOutsideTheBank or
  BillPaidOutsideBank [reconciliation], with zero implicit crossings
  and zero bleeds. Nobody fell into the loop. No arm cost more than
  A; D's median was 14% under it.
- **Round 3** (hard with the hint, B/D): all 10 passed with the
  arrow. B+hint cost +38% over round 2's B, D+hint +17% over D. Its
  wall time was inflated by machine load (medians near 8.7 minutes
  against 4 to 5 in round 2 for the same work); minutes are not
  reliable in this round.

### Where B's round-1 premium came from

From the transcripts (`round2-builder-reports.md`):

1. **Rewriting the gate.** 3 of 5 B control runs edited
   `flow_test.go`, the skill and AGENTS.md. A correction (caused by
   the change it corrects) was neither a direct landing nor a
   policy, so `CorrectExpense` never showed in `docs/flow.md`; the
   runs read that as a bug in the gate and patched it. The biggest
   single cost, and a correctness risk: agents changed the check
   that was meant to hold them.
2. **Reading the rule.** About 3 extra file reads per run (the skill,
   `docs/flow.md`, the 300-line extractor), carried in context on
   every later turn: 10 to 14k more tokens per turn.
3. **Learning to run `make flow`**, a separate regeneration step.

No B run failed `make ci` on the flow check. On the hard ask, with
no correction in play and a pattern to copy, B was not more
expensive than A.

### Vocabulary drift

The downstream fact was named four ways on the cross ask (15 runs):

| Name | Runs |
|---|---|
| MemberLeftBill | 6 |
| BillShareWithdrawn | 6 |
| BillSplitChanged (an existing reason, reused) | 2 |
| MemberTakenOffBill | 1 |

On the hard ask (30 runs) it was named two ways:
BillPaidOutsideTheBank 23, BillPaidOutsideBank 7. Without the hint
16 and 4; with it 7 and 3. The hint did not reduce it. Every arm
drifted except round 2's D (5 of 5 BillPaidOutsideTheBank), which at
n=5 is not a finding.

## Findings

1. **The chain's shape was already held** by what the repository
   had: the AGENTS.md rule that a context moves another by causing a
   change there, the `noBleed` check, and the two existing examples.
   The baseline got the shape right every time.
2. **Enforcement caught nothing** in 35 enforced runs (B, C, B-opt,
   B+hint), and once backfired: agents edited the gate.
3. **Structure given upfront (C) or a prompt hint gave no measurable
   gain** at real token cost (B+hint +38% over B, D+hint +17% over
   D).
4. **D gives the same map at baseline cost.**
5. **What varies is the words, not the shape.**

## Limits

- 5 runs per cell: only large effects are visible.
- Every ask was solvable by copying an existing pattern. An ask with
  no precedent might separate the arms.
- The hidden tests check database rows, not the UI.
- The same model ran every run.
- B bundles extraction with rules, so B against A does not separate
  the cost of the map from the cost of the rules. D against A does,
  on the hard ask only.

## Held in reserve

Each with the trigger that brings it in:

- **Blocking rules**, in B-opt's version (the cheapest known: rule
  inline in AGENTS.md, fix printed under every failure, corrections
  shown in the doc). Trigger: the first time a `docs/flow.md` diff
  shows a wrong shape that review missed.
- **The same-request parent rule for corrections** (a cause counts
  as a parent only if it comes from the same request), which makes
  R2 stricter. Only if blocking is ever adopted.
- **Suggesting exact command and event names in asks.** Likely to
  raise reuse, not measured. A 5-run D round with named hints would
  settle it.

## Where this goes next

The owner's plan:

- Event storming with the broad team and impact mapping stay the
  business practices. Their big-picture event output is what gets
  hinted into asks.
- What the code actually does is extracted, never intervened on, and
  rendered onto a Blueprint board as an event-storming view with
  only these elements: commands, bounded contexts, external systems
  (the bank, the schedule), events and policies. It is for display
  only, aimed at a developer joining the team a year in.
- `docs/flow.json` is the source for that rendering. Its shape is
  plain and versioned (`version`, `entry_points`, `unnamed`,
  `policies`, `chains`, `empty`, `mixed`, `implicit_crossings`).

### Second iteration (not built)

Both items follow the same line as the decision: the name is
recorded where the thing happens, and extraction does the rest.
Nothing is enforced.

1. **External systems, in both directions.** On the board they are
   boxes, not stickies; people may show as actors.
   - **Inbound**: the bank webhook and the schedule tick. The data is
     already recorded: the Trail's actor (`member:<id>`, `human`,
     `agent:bank`, `agent:schedule`). `flow.json` already carries
     each entry point's actors with the member id dropped (`member`,
     `human`, `agent:bank`). Not built: the trigger kind (person, or
     external system and its name), derived from the actor with
     nothing new declared.
   - **Outbound**: HTTP calls the app makes to other services. There
     are none today; the bill reminder is a log line because there is
     no mail transport. When one exists it is extracted the same way:
     the app's outbound `http.Client` gets a named transport (otelhttp's
     gives a span per call; it is not a dependency yet), and the
     scenario collector records each outbound call against the entry
     point and the change that caused it. Blueprint models this as a
     policy or system that "calls" an external system.
2. **Invariants.** The household's rules already have names:
   SplitEqually, SplitByShares, MatchByName, NoSettlementUnderDispute,
   SplitAppliesForward, and PaidOutsideTheBank in the hard ask's
   reference. Today those names live only in AGENTS.md, code comments
   and scenario names, so nothing can extract them. Blueprint has an
   `invariant` node type, as PV3 does. There are two kinds, and each
   needs its name recorded:
   - **Shaping rules** decide what lands (SplitEqually, MatchByName).
     The Trail could carry the rule beside the reason, for example
     `Because(reason, cause).By(rule)`, stored as a `rule` column on
     `changes`. `flow.json` then shows ShareAssigned by SplitEqually.
   - **Refusing rules** stop a write (NoSettlementUnderDispute). A
     refusal lands no change row, so it needs its own record: a span
     event or log line naming the rule and the entry point, which the
     scenario collector picks up.

## Supersedes nothing

It refines ADR 0001's "a board, when needed, is drawn on the spot
from `changes` or the scenarios": the board's source is now
generated by `make flow`, still never maintained by hand.
