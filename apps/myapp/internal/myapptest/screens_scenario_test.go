package myapptest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Scenarios for the screens around the forms: the page a member fills
// in before each POST the other scenarios send, and the way in and out
// of the instance. None of them lands anything: looking at a form, or
// signing in and out, is not a fact about the instance. The route gate
// (coverage_test.go) is what asks for them: every mounted route is
// reached by a scenario.

func TestScenarioEveryFormIsAScreenThatLandsNothing(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	n := s.note("Groceires")
	s.mark_()
	// When Ana opens each form a member fills in
	ana.sees("/notes/new", "Write a note")
	ana.sees("/notes/"+itoa(n.ID)+"/correct", "Correct a note", "Groceires")
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
	// Then her old session no longer opens the notes
	if rec := ana.get("/"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/signin") {
		t.Fatalf("signed out, GET /: status %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	// And a visitor sees the way in and the way to join
	for target, says := range map[string]string{"/signin": "Sign in", "/signup": "Your name"} {
		if rec := get(t, s.h, target); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), says) {
			t.Fatalf("a visitor GET %s: status %d, %q missing", target, rec.Code, says)
		}
	}
	// When she signs back in
	rec := post(t, s.h, "/signin", url.Values{"email": {ana.Email}, "password": {"correct horse battery"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("sign in: status %d\n%s", rec.Code, rec.Body.String())
	}
	ana.cookie = nil
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 && c.Value != "" {
			ana.cookie = c
		}
	}
	if ana.cookie == nil {
		t.Fatal("sign in: no session cookie")
	}
	// Then she is home again, and neither door landed anything
	ana.sees("/", "Notes", "Welcome, Ana")
	s.changed()
}
