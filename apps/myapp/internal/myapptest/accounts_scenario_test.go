package myapptest

import (
	"encoding/json"
	"testing"

	myapp "myapp/internal/myapp"
)

// Given an empty instance, When Ana signs up, Then she is a member,
// the record says she joined and carries no password, and the notes
// context greets her: a welcome note caused by her joining, the
// example's cross-context policy, written by the rule
// EveryNewMemberIsWelcomed. /changes says so (by rule) and
// /changes.json carries it as rule, a key the join's record, which no
// rule shaped, does not have.
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
	if got[0].Rule != "" || got[1].Rule != myapp.RuleEveryNewMemberIsWelcomed {
		t.Fatalf("rules: MemberJoined %q, want none; WelcomeNoteWritten %q, want %q", got[0].Rule, got[1].Rule, myapp.RuleEveryNewMemberIsWelcomed)
	}
	ana.sees("/", "Notes", "Welcome, Ana")
	ana.sees("/changes", "by rule "+myapp.RuleEveryNewMemberIsWelcomed)

	rec := ana.get("/changes.json")
	var feed struct {
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &feed); err != nil || len(feed.Changes) != 2 {
		t.Fatalf("/changes.json: %v\n%s", err, rec.Body.String())
	}
	joined, welcomed := feed.Changes[0], feed.Changes[1]
	if joined["reason"] != myapp.ReasonMemberJoined || welcomed["reason"] != myapp.ReasonWelcomeNoteWritten {
		t.Fatalf("/changes.json: reasons %v, %v", joined["reason"], welcomed["reason"])
	}
	if rule, ok := joined["rule"]; ok {
		t.Fatalf("MemberJoined in /changes.json: rule = %v, want no rule key", rule)
	}
	if welcomed["rule"] != myapp.RuleEveryNewMemberIsWelcomed {
		t.Fatalf("WelcomeNoteWritten in /changes.json: rule = %v, want %q", welcomed["rule"], myapp.RuleEveryNewMemberIsWelcomed)
	}
}
