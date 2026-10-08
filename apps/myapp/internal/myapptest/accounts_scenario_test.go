package myapptest

import (
	"testing"

	myapp "myapp/internal/myapp"
)

// Given an empty instance, When Ana signs up, Then she is a member,
// the record says she joined, and the record carries no password.
func TestScenarioJoining(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	got := s.changed(myapp.ReasonMemberJoined)
	if got[0].Actor != "human" || got[0].EntityID != ana.id() {
		t.Fatalf("MemberJoined = %+v", got[0])
	}
	if want := `"PasswordHash":""`; !contains(got[0].Diff, want) {
		t.Fatalf("the MemberJoined diff carries a secret: %s", got[0].Diff)
	}
	ana.sees("/", "Notes", "Ana")
}
