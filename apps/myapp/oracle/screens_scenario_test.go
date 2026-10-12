//go:build oracle

package oracle_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The screens around the forms, and the way in and out: none of them
// lands anything.

func TestScenarioEveryFormIsAScreenThatLandsNothing(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	n := s.note("Groceires")
	s.mark_()
	// When Ana opens each form a member fills in
	ana.sees("/notes/new", "Write a note")
	ana.sees("/notes/"+itoa(n)+"/correct", "Correct a note", "Groceires")
	// Then nothing has happened on the instance
	s.changed()
}

func TestScenarioAMemberSignsOutAndBackIn(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()
	// When Ana signs out
	if rec := ana.post("/signout", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("sign out: status %d", rec.Code)
	}
	// Then her browser no longer opens the notes
	if rec := ana.get("/"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header.Get("Location"), "/signin") {
		t.Fatalf("signed out, GET /: status %d to %q", rec.Code, rec.Header.Get("Location"))
	}
	// And a visitor sees the way in and the way to join
	v := s.visitor()
	v.sees("/signin", "Sign in")
	v.sees("/signup", "Your name")
	// When she signs back in
	if rec := ana.post("/signin", url.Values{"email": {ana.Email}, "password": {"correct horse battery"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("sign in: status %d\n%s", rec.Code, rec.Body)
	}
	// Then she is home again, and neither door landed anything
	ana.sees("/", "Notes", "Welcome, Ana")
	s.changed()
}

// The change log a machine reads is the one the screen shows: the
// oracle's own Then reads it, and a signed-out browser cannot.
func TestScenarioTheChangeLogIsReadableByAMachine(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	got := s.changed(ReasonMemberJoined, ReasonWelcomeNoteWritten)
	ana.sees("/changes", ReasonMemberJoined, ReasonWelcomeNoteWritten, got[1].Cause)
	if rec := s.visitor().get("/changes.json"); rec.Code == http.StatusOK {
		t.Fatalf("signed-out read of the change feed: status %d", rec.Code)
	}
}
