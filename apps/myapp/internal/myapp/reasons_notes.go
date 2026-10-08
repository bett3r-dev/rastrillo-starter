package myapp

// The notes context, the example: what members wrote down, and the
// corrections that put a note right. Its vocabulary lands on the
// tables it owns and on no other. The glossary is CONTEXT-notes.md.

const ContextNotes = "notes"

var notesTables = []string{"notes"}

const (
	// notes
	ReasonNoteWritten = "NoteWritten"
	// NoteCorrected is a note's text put right. Its cause is the
	// change it corrects, so /changes links the two; the row's fields
	// are written forward and the record keeps both versions.
	ReasonNoteCorrected = "NoteCorrected"
)

var notesReasons = []string{
	ReasonNoteWritten,
	ReasonNoteCorrected,
}
