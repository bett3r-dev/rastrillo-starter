package myapptest

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// The binding test: the reason vocabulary (internal/myapp/reasons.go)
// and the scenarios in this package hold each other. Every declared
// reason must appear in at least one scenario's Then; a reason a Then
// names that is not declared does not compile. Codes nobody specified
// cannot exist; codes nobody exercises fail the suite.
func TestEveryReasonIsSpecifiedAndExercised(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*_scenario_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenario files: %v", err)
	}
	used := map[string]bool{}
	re := regexp.MustCompile(`myapp\.Reason([A-Z][A-Za-z0-9]*)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
	}
	var missing []string
	for _, r := range myapp.Reasons {
		if !used[r] {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("declared in reasons.go but no scenario's Then lands them: %s\nWrite the scenario or delete the reason.", strings.Join(missing, ", "))
	}
	for u := range used {
		found := false
		for _, r := range myapp.Reasons {
			if r == u {
				found = true
			}
		}
		if !found {
			t.Fatalf("Reason%s is used by a scenario but not in myapp.Reasons", u)
		}
	}
}
