package myapp

// The notes context, the example: what members wrote down, the
// corrections that put a note right, and the welcome note it writes
// when the accounts context says someone joined. Its vocabulary lands
// on the tables it owns and on no other. The glossary is
// CONTEXT-notes.md.

const ContextNotes = "notes"

var notesTables = []string{"notes"}

const (
	// notes
	ReasonNoteWritten = "NoteWritten"
	// NoteCorrected is a note's text put right. Its cause is the
	// change it corrects, so /changes links the two; the row's fields
	// are written forward and the record keeps both versions.
	ReasonNoteCorrected = "NoteCorrected"
	// WelcomeNoteWritten is the note that greets a member who joined:
	// this context's policy on the accounts context's MemberJoined, so
	// its cause is that change (the example's cross-context arrow).
	ReasonWelcomeNoteWritten = "WelcomeNoteWritten"
)

// The notes context's invariants: each a rule in the business's words,
// with the reasons it guards (may refuse) or shapes. A refusal names
// one with Trail.Refuse; TestEveryInvariantIsSpecifiedAndExercised
// holds each to a scenario's Then.
const (
	// ACorrectionChangesSomething refuses a correction that leaves the
	// note's title and body as they were: nothing would be put right.
	RuleACorrectionChangesSomething = "ACorrectionChangesSomething"
	// EveryNewMemberIsWelcomed shapes the welcome note: the policy's
	// write names it (Trail.By), so the record says which rule wrote
	// the note nobody asked for.
	RuleEveryNewMemberIsWelcomed = "EveryNewMemberIsWelcomed"
)

var notesInvariants = []Invariant{
	{Name: RuleACorrectionChangesSomething, Context: ContextNotes,
		Rule:   "A correction must change the note's title or its body.",
		Guards: []string{ReasonNoteCorrected}},
	{Name: RuleEveryNewMemberIsWelcomed, Context: ContextNotes,
		Rule:   "Every member who joins gets a welcome note.",
		Shapes: map[string][]string{ReasonWelcomeNoteWritten: {"notes"}}},
}

var notesReasons = []string{
	ReasonNoteWritten,
	ReasonNoteCorrected,
	ReasonWelcomeNoteWritten,
}

// notesWrites is what each reason may write: per table, the columns
// its change's diff may carry (changes.Policy.Allow). A write outside
// it is refused (ErrReasonMismatch). A correction writes the text
// forward and nothing else: who wrote the note stays who wrote it.
var notesWrites = map[string]Writes{
	ReasonNoteWritten: {
		"notes": {"id", "author_id", "title", "body", "created_at", "updated_at"},
	},
	ReasonNoteCorrected: {
		"notes": {"title", "body"},
	},
	ReasonWelcomeNoteWritten: {
		"notes": {"id", "author_id", "title", "body", "created_at", "updated_at"},
	},
}
