package myapptest

import (
	"testing"

	myapp "myapp/internal/myapp"
)

// Given an empty instance, When Ana signs up, Then she is a member,
// the record says she joined and carries no password, and the notes
// context greets her: a welcome note caused by her joining, the
// example's cross-context policy.
func TestScenarioJoining(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	got := s.changed(myapp.ReasonMemberJoined, myapp.ReasonWelcomeNoteWritten)
	if got[0].Actor != "human" || got[0].EntityID != ana.id() || got[0].EntryPoint != "Join" {
		t.Fatalf("MemberJoined = %+v", got[0])
	}
	if want := `"PasswordHash":""`; !contains(got[0].Diff, want) {
		t.Fatalf("the MemberJoined diff carries a secret: %s", got[0].Diff)
	}
	if got[1].Cause != got[0].Ref() || got[1].EntryPoint != "Join" {
		t.Fatalf("WelcomeNoteWritten is not caused by the join: %+v", got[1])
	}
	ana.sees("/", "Notes", "Welcome, Ana")
}
