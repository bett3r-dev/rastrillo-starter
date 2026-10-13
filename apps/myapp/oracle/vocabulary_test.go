//go:build oracle

package oracle_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// The oracle's own copy of the vocabulary: the reasons a change may
// carry, the invariants a refusal may name, and which context owns
// each reason, invariant and table. It is the
// specification a binary is judged by, so it is written out here
// rather than asked of the binary (/changes.json states no context)
// or read off the Go code (another implementation has none).
// TestTheOracleSpeaksTheAppsVocabulary holds it to reasons_*.go while
// the Go app is the reference.

const (
	ctxAccounts = "accounts"
	ctxNotes    = "notes"
)

const (
	ReasonMemberJoined = "MemberJoined"

	ReasonNoteWritten        = "NoteWritten"
	ReasonNoteCorrected      = "NoteCorrected"
	ReasonWelcomeNoteWritten = "WelcomeNoteWritten"
)

var reasonContext = map[string]string{
	ReasonMemberJoined: ctxAccounts,

	ReasonNoteWritten:        ctxNotes,
	ReasonNoteCorrected:      ctxNotes,
	ReasonWelcomeNoteWritten: ctxNotes,
}

// The invariants: each rule's name, and the context that declares it.
const (
	RuleACorrectionChangesSomething = "ACorrectionChangesSomething"
	RuleEveryNewMemberIsWelcomed    = "EveryNewMemberIsWelcomed"
)

var ruleContext = map[string]string{
	RuleACorrectionChangesSomething: ctxNotes,
	RuleEveryNewMemberIsWelcomed:    ctxNotes,
}

var tableContext = map[string]string{
	"members": ctxAccounts,

	"notes": ctxNotes,
}

// TestTheOracleSpeaksTheAppsVocabulary: the copy above says exactly
// what reasons_*.go declares, reason for reason, invariant for
// invariant and table for table.
// A reason added to the app without its line here fails, as does one
// moved between contexts.
func TestTheOracleSpeaksTheAppsVocabulary(t *testing.T) {
	t.Parallel()
	for _, r := range myapp.Reasons {
		if reasonContext[r] != myapp.ContextOfReason(r) {
			t.Errorf("reason %s: oracle says %q, reasons_*.go says %q", r, reasonContext[r], myapp.ContextOfReason(r))
		}
	}
	for r := range reasonContext {
		if !slices.Contains(myapp.Reasons, r) {
			t.Errorf("reason %s is in the oracle but not declared in reasons_*.go", r)
		}
	}
	n := 0
	for ctx, tables := range myapp.Contexts {
		for _, tb := range tables {
			n++
			if tableContext[tb] != ctx {
				t.Errorf("table %s: oracle says %q, reasons_*.go says %q", tb, tableContext[tb], ctx)
			}
		}
	}
	if n != len(tableContext) {
		t.Errorf("the oracle names %d tables, reasons_*.go %d", len(tableContext), n)
	}
	for _, inv := range myapp.Invariants {
		if ruleContext[inv.Name] != inv.Context {
			t.Errorf("invariant %s: oracle says %q, reasons_*.go says %q", inv.Name, ruleContext[inv.Name], inv.Context)
		}
	}
	if len(ruleContext) != len(myapp.Invariants) {
		t.Errorf("the oracle names %d invariants, reasons_*.go %d", len(ruleContext), len(myapp.Invariants))
	}
}

// TestEveryReasonIsExercisedHere is the binding test's twin for the
// oracle: every reason in the vocabulary appears in some scenario's
// Then here, so the black-box suite specifies no less than the
// in-process one does.
func TestEveryReasonIsExercisedHere(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*_scenario_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario files: %v", err)
	}
	used := map[string]bool{}
	re := regexp.MustCompile(`\bReason([A-Z][A-Za-z0-9]*)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			used["Reason"+m[1]] = true
		}
	}
	var missing []string
	for r := range reasonContext {
		if !used["Reason"+r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("no oracle scenario's Then lands: %s", strings.Join(missing, ", "))
	}
}
