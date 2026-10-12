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
