package myapptest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// A context reads another's data by id lookup, or reacts to its
// changes; it never joins across. This reads the app's source, which
// Go cannot see as a test input, so it runs under its own make
// target (make contexts) with -count=1.
func TestNoJoinsAcrossContexts(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../myapp/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no app sources: %v", err)
	}
	join := regexp.MustCompile(`(?i)\bJoins\(|\bJOIN\b`)
	word := regexp.MustCompile(`[a-z_]+`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !join.MatchString(line) {
				continue
			}
			seen := map[string]bool{}
			for _, w := range word.FindAllString(strings.ToLower(line), -1) {
				if ctx := myapp.ContextOfTable(w); ctx != "" {
					seen[ctx] = true
				}
			}
			if len(seen) > 1 {
				t.Errorf("%s:%d joins across contexts: %s", filepath.Base(f), i+1, strings.TrimSpace(line))
			}
		}
	}
}
