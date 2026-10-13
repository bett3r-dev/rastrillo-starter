package myapptest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
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

	got := s.changed(myapp.ReasonNoteWritten)
	if got[0].Actor != "member:"+itoa(ana.id()) {
		t.Fatalf("actor = %q, want Ana", got[0].Actor)
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
	s.mark_()
	written := s.note("Groceires")

	redirected(t, ana.correctsNote(written.ID, "Groceries", "Milk"), "/")

	got := s.changed(myapp.ReasonNoteCorrected)
	var first []int64
	s.db.G.Table("changes").Where("reason = ?", myapp.ReasonNoteWritten).Pluck("id", &first)
	if got[0].Cause != "change:"+itoa(first[0]) {
		t.Fatalf("correction cause = %q, want the NoteWritten change:%d", got[0].Cause, first[0])
	}
	ana.sees("/", "Groceries")
	ana.sees("/changes", myapp.ReasonNoteCorrected, `href="#change-`+itoa(first[0])+`"`)
}

// Given a member's note, When she posts a correction that changes
// neither its title nor its body, Then ACorrectionChangesSomething
// refuses it: 422 with the rule's name as data-rule, nothing lands,
// and one refused line names the rule, its context, the entry point,
// her and the request's trace id (the response's X-Correlation-Id).
func TestScenarioAnUnchangedCorrectionIsRefused(t *testing.T) {
	t.Parallel()
	s := givenLogged(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceries", "milk"), "/")
	n := s.note("Groceries")
	s.mark_()

	rec := ana.correctsNote(n.ID, "Groceries", "milk")

	s.refusedBy(rec, myapp.RuleACorrectionChangesSomething)
	if !contains(rec.Body.String(), `<p role="alert" data-rule="ACorrectionChangesSomething">That correction changes nothing: edit the title or the body.</p>`) {
		t.Fatalf("the refusal's alert is not on the page:\n%s", rec.Body.String())
	}
	lines := s.logged("refused")
	if len(lines) != 1 {
		t.Fatalf("refused lines = %d, want 1: %v", len(lines), lines)
	}
	trace := rec.Header().Get("X-Correlation-Id")
	if trace == "" {
		t.Fatal("the response carries no X-Correlation-Id")
	}
	for k, want := range map[string]string{
		"rule":        "ACorrectionChangesSomething",
		"context":     "notes",
		"entry_point": "CorrectNote",
		"actor":       "member:" + itoa(ana.id()),
		"correlation": trace,
		"level":       "INFO",
	} {
		if got := lines[0][k]; got != want {
			t.Errorf("refused line %s = %v, want %q (line %v)", k, got, want, lines[0])
		}
	}
	ana.sees("/", "Groceries", "milk")
}

// Given a member, When she writes a note and corrects it, Then both
// changes are within that note ("note:<id>") and the note's history
// reads back through changes.InScope, the write first. The write's own
// change carries the scope even though the note's id only exists once
// it is inserted (Trail.WithinCreated).
func TestScenarioANoteKeepsItsOwnHistory(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()

	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	n := s.note("Groceires")
	redirected(t, ana.correctsNote(n.ID, "Groceries", "Milk"), "/")

	got := s.changed(myapp.ReasonNoteWritten, myapp.ReasonNoteCorrected)
	scope := "note:" + itoa(n.ID)
	history, err := changes.InScope(context.Background(), s.db.G, scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].ID != got[0].ID || history[1].ID != got[1].ID {
		t.Fatalf("InScope(%s) = %v, want the write then the correction", scope, history)
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
	// A form error names no rule (CONTEXT-notes.md, ACorrectionChangesSomething's "Not").
	if body := rec.Body.String(); contains(body, "data-rule") {
		t.Fatalf("a form error carries a data-rule, want none\n%s", body)
	}
	s.changed()
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
