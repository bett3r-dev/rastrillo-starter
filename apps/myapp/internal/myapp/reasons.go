package myapp

import (
	"slices"

	"myproject/libs/changes"
)

// The reason vocabulary: every business-language name a change record
// may carry, declared per context in reasons_<context>.go. Those
// files ARE the event storm's output. Adding a reason without a
// scenario that lands it fails TestEveryReasonIsSpecifiedAndExercised
// in internal/myapptest; a scenario naming one not declared does not
// compile.
//
// Name the fact, in the customer's words, past tense: what happened,
// never what the code did.
//
// One table, one vocabulary: a reason lands only on tables of its own
// context (noBleed, on every scenario's Then). A context moves
// another's data by causing a change there, never by writing its
// tables.

// Reasons is the declared vocabulary, for the binding test and for
// anyone drawing a board from it.
var Reasons = slices.Concat(accountsReasons, notesReasons)

// Contexts is each bounded context's tables. The context of a table
// is declared here, not in its name; a new table gets its context's
// prefix (notes_*) unless it is the context's namesake.
var Contexts = map[string][]string{
	ContextAccounts: accountsTables,
	ContextNotes:    notesTables,
}

// Writes is one reason's declaration of what it may write: table ->
// the columns its change's diff may carry. Declared per context in
// reasons_<context>.go, beside the reasons.
type Writes = changes.Writes

// ReasonWrites is every reason's Writes, the allow-list changes.Install
// enforces (Policy.Allow): a write whose table or columns the reason
// does not declare is refused (ErrReasonMismatch). TestEveryReasonDeclaresWhatItWrites
// holds it to the vocabulary.
var ReasonWrites = map[string]Writes{}

// Invariant is one business rule a context declares beside its
// reasons, in reasons_<context>.go.
type Invariant = changes.Invariant

// Invariants is every context's declared rules, installed as
// changes.Policy.Invariants: a refusal must name one of them
// (Trail.Refuse), and a write one shapes must name it (Trail.By).
var Invariants = slices.Concat(notesInvariants)

var reasonContexts = map[string]string{}

func init() {
	for _, r := range accountsReasons {
		reasonContexts[r] = ContextAccounts
	}
	for _, r := range notesReasons {
		reasonContexts[r] = ContextNotes
	}
	for _, w := range []map[string]Writes{accountsWrites, notesWrites} {
		for r, tables := range w {
			ReasonWrites[r] = tables
		}
	}
}

// ContextOfReason is the context that declared a reason; "" for an
// undeclared one.
func ContextOfReason(reason string) string { return reasonContexts[reason] }

// ContextOfTable is the context that owns a table; "" for a table no
// context owns (plumbing).
func ContextOfTable(table string) string {
	for ctx, tables := range Contexts {
		if slices.Contains(tables, table) {
			return ctx
		}
	}
	return ""
}
