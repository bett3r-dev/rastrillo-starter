//go:build oracle

package oracle_test

import (
	"net/http"
	"testing"
)

// Given a member, When she writes a note, Then everyone sees it and
// the record says it was written, by her.
func TestScenarioWritingANote(t *testing.T) {
	t.Parallel()
	s := given(t)
	people := s.members("Ana", "Luis")
	ana, luis := people[0], people[1]
	s.mark_()

	redirected(t, ana.writesNote("Groceries", "Milk and bread"), "/")

	got := s.changed(ReasonNoteWritten)
	if got[0].Actor != "member:"+itoa(ana.id()) || got[0].EntryPoint != "WriteNote" {
		t.Fatalf("NoteWritten = %+v, want Ana's WriteNote", got[0])
	}
	luis.sees("/", "Groceries", "Milk and bread", "Ana")
}

// Given a written note, When someone corrects it, Then the note reads
// the correction, the record keeps both and the correction's cause is
// the change it corrects, one click away on /changes.
func TestScenarioCorrectingANote(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	written := s.changed(ReasonMemberJoined, ReasonWelcomeNoteWritten, ReasonNoteWritten)[2]

	redirected(t, ana.correctsNote(written.RowID, "Groceries", "Milk"), "/")

	got := s.changed(ReasonNoteCorrected)
	if got[0].Cause != written.Ref() || got[0].EntryPoint != "CorrectNote" {
		t.Fatalf("correction = %+v, want caused by %s", got[0], written.Ref())
	}
	ana.sees("/", "Groceries")
	ana.sees("/changes", ReasonNoteCorrected, `href="#change-`+itoa(written.ID)+`"`)
}

// Every change on a note is within it: the write and its correction
// both carry "note:<id>", the write's own change included, though the
// note's id only exists once it is inserted.
func TestScenarioANoteKeepsItsOwnHistory(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()

	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	n := s.note("Groceires")
	redirected(t, ana.correctsNote(n, "Groceries", "Milk"), "/")

	got := s.changed(ReasonNoteWritten, ReasonNoteCorrected)
	scope := "note:" + itoa(n)
	for _, c := range got {
		if c.Scope != scope {
			t.Fatalf("%s is within %q, want %q", c.Reason, c.Scope, scope)
		}
	}
	ana.sees("/changes", `within <code class="rst-mono">`+scope+`</code>`)
}

// A note needs a title; the form says so beside the field and nothing
// lands.
func TestScenarioANoteWithoutATitleIsRefused(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()

	rec := ana.writesNote("", "No title")

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	s.changed()
}
