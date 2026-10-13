package myapptest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
)

// The invariant binding test, binding_test.go's twin for the rules:
// every invariant in myapp.Invariants appears in some scenario's Then
// (refused by it, or a change carrying it), named by its constant
// myapp.Rule<Name>; and a scenario naming a Rule constant that is not
// in myapp.Invariants fails. A Rule constant nobody declared does not
// compile. Rules nobody specified cannot exist; rules nobody exercises
// fail the suite.
func TestEveryInvariantIsSpecifiedAndExercised(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*_scenario_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario files: %v", err)
	}
	var sources []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, string(b))
	}
	unbound, undeclared := bindRules(sources, myapp.Invariants)
	if len(unbound) > 0 {
		t.Errorf("declared in myapp.Invariants but no scenario's Then names them: %s\nWrite the scenario or delete the invariant.", strings.Join(unbound, ", "))
	}
	if len(undeclared) > 0 {
		t.Errorf("named by a scenario but not in myapp.Invariants: %s", strings.Join(undeclared, ", "))
	}
}

var ruleRef = regexp.MustCompile(`myapp\.Rule([A-Z][A-Za-z0-9]*)`)

// bindRules is the binding both ways: the declared invariants no
// source names (unbound), and the rules a source names that none
// declares (undeclared), each sorted.
func bindRules(sources []string, declared []changes.Invariant) (unbound, undeclared []string) {
	used := map[string]bool{}
	for _, src := range sources {
		for _, m := range ruleRef.FindAllStringSubmatch(src, -1) {
			used[m[1]] = true
		}
	}
	names := map[string]bool{}
	for _, inv := range declared {
		names[inv.Name] = true
		if !used[inv.Name] {
			unbound = append(unbound, inv.Name)
		}
	}
	for u := range used {
		if !names[u] {
			undeclared = append(undeclared, u)
		}
	}
	sort.Strings(unbound)
	sort.Strings(undeclared)
	return unbound, undeclared
}

// The binding fails both ways: a declared invariant no scenario names
// is unbound, a rule a scenario names that is not declared is
// undeclared, and the two are told apart.
func TestTheInvariantBindingFailsBothWays(t *testing.T) {
	t.Parallel()
	declared := []changes.Invariant{{Name: "AKept"}, {Name: "BUnused"}}
	src := `s.refusedBy(rec, myapp.RuleAKept); s.refusedBy(rec, myapp.RuleCStray)`
	unbound, undeclared := bindRules([]string{src}, declared)
	if !slices.Equal(unbound, []string{"BUnused"}) || !slices.Equal(undeclared, []string{"CStray"}) {
		t.Fatalf("unbound %v, undeclared %v; want [BUnused] and [CStray]", unbound, undeclared)
	}
	if unbound, undeclared := bindRules([]string{src + " myapp.RuleBUnused"}, append(declared, changes.Invariant{Name: "CStray"})); len(unbound)+len(undeclared) != 0 {
		t.Fatalf("every rule declared and named: unbound %v, undeclared %v; want none", unbound, undeclared)
	}
}

// Every invariant is its own context's: a unique, non-empty name and a
// rule sentence, a declared context, and only that context's reasons
// in Guards and Shapes, shaping only that context's tables. Install
// does not check an invariant's context (decisions D14), so this does.
func TestEveryInvariantBelongsToItsContext(t *testing.T) {
	t.Parallel()
	if faults := invariantFaults(myapp.Invariants); len(faults) > 0 {
		t.Fatalf("invariants:\n  %s", strings.Join(faults, "\n  "))
	}
}

// invariantFaults is every way invs breaks TestEveryInvariantBelongsToItsContext's rule.
func invariantFaults(invs []changes.Invariant) []string {
	var faults []string
	seen := map[string]bool{}
	for _, inv := range invs {
		if inv.Name == "" || seen[inv.Name] {
			faults = append(faults, "an invariant's name is empty or repeated: "+inv.Name)
		}
		seen[inv.Name] = true
		if inv.Rule == "" {
			faults = append(faults, inv.Name+" has no rule sentence")
		}
		if _, ok := myapp.Contexts[inv.Context]; !ok {
			faults = append(faults, inv.Name+" is in no declared context: "+inv.Context)
		}
		reasons := slices.Clone(inv.Guards)
		for r, tables := range inv.Shapes {
			reasons = append(reasons, r)
			for _, tb := range tables {
				if myapp.ContextOfTable(tb) != inv.Context {
					faults = append(faults, inv.Name+" shapes "+tb+", not a "+inv.Context+" table")
				}
			}
		}
		for _, r := range reasons {
			if myapp.ContextOfReason(r) != inv.Context {
				faults = append(faults, inv.Name+" names "+r+", not a "+inv.Context+" reason")
			}
		}
	}
	sort.Strings(faults)
	return faults
}

// The context check fails on each fault it names, and passes a sound
// declaration.
func TestTheInvariantContextCheckFails(t *testing.T) {
	t.Parallel()
	sound := changes.Invariant{Name: "A", Context: myapp.ContextNotes, Rule: "r", Guards: []string{myapp.ReasonNoteCorrected},
		Shapes: map[string][]string{myapp.ReasonWelcomeNoteWritten: {"notes"}}}
	if f := invariantFaults([]changes.Invariant{sound}); len(f) != 0 {
		t.Fatalf("a sound invariant: %v", f)
	}
	for _, c := range []struct {
		name string
		edit func(*changes.Invariant)
		want string
	}{
		{"empty name", func(i *changes.Invariant) { i.Name = "" }, "empty or repeated"},
		{"no rule", func(i *changes.Invariant) { i.Rule = "" }, "no rule sentence"},
		{"unknown context", func(i *changes.Invariant) { i.Context = "billing" }, "no declared context"},
		{"foreign guard", func(i *changes.Invariant) { i.Guards = []string{myapp.ReasonMemberJoined} }, "names MemberJoined"},
		{"foreign shaped reason", func(i *changes.Invariant) { i.Shapes = map[string][]string{myapp.ReasonMemberJoined: {"notes"}} }, "names MemberJoined"},
		{"foreign table", func(i *changes.Invariant) {
			i.Shapes = map[string][]string{myapp.ReasonWelcomeNoteWritten: {"members"}}
		}, "shapes members"},
	} {
		inv := sound
		c.edit(&inv)
		if f := strings.Join(invariantFaults([]changes.Invariant{inv}), "; "); !strings.Contains(f, c.want) {
			t.Errorf("%s: faults %q, want one naming %q", c.name, f, c.want)
		}
	}
	if f := strings.Join(invariantFaults([]changes.Invariant{sound, sound}), "; "); !strings.Contains(f, "repeated") {
		t.Errorf("a repeated name: faults %q", f)
	}
}
