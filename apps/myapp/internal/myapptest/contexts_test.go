package myapptest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// appSources is every non-test Go file of the app: root
// (internal/myapp) and every package under it.
func appSources(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("../myapp", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("no app sources: %v", err)
	}
	return files
}

// A context reads another's data by id lookup, or reacts to its
// changes; it never joins across. This reads the app's source, root
// and feature packages alike, which Go cannot see as a test input, so
// it runs under its own make target (make contexts) with -count=1.
func TestNoJoinsAcrossContexts(t *testing.T) {
	t.Parallel()
	join := regexp.MustCompile(`(?i)\bJoins\(|\bJOIN\b`)
	word := regexp.MustCompile(`[a-z_]+`)
	for _, f := range appSources(t) {
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
				t.Errorf("%s:%d joins across contexts: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// appPackage is the import path of root, the registry; every package
// under it is a feature, apart from the shared ones.
const appPackage = "myapp/internal/myapp"

// sharedPackages are the packages under internal/myapp that are not
// features: what every feature may import, and which import no
// feature themselves. kit, once an app has features, is the request
// glue (Trail, render, role gate); icons is the vendored icon set the
// layout draws from.
var sharedPackages = map[string]bool{"kit": true, "icons": true}

// Features never import each other, nor root (Go would refuse that
// cycle anyway, root mounting them), and a shared package imports no
// feature, so no two features meet through it. A policy from one
// feature to another goes through root: the reacting context exports
// a function, the source takes it in its Deps, and root wires them
// (in blueprint2, policies.go). This reads the app's source, so it runs under make
// contexts with -count=1.
func TestNoFeatureImportsAnother(t *testing.T) {
	t.Parallel()
	for _, f := range appSources(t) {
		rel, err := filepath.Rel("../myapp", filepath.Dir(f))
		if err != nil {
			t.Fatal(err)
		}
		if rel == "." {
			continue // root, the registry, imports every feature
		}
		self := strings.Split(filepath.ToSlash(rel), "/")[0]
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == appPackage {
				t.Errorf("%s imports root (%s): root is the registry, and a feature gets what it needs in its Deps", f, path)
				continue
			}
			rest, ok := strings.CutPrefix(path, appPackage+"/")
			if !ok {
				continue
			}
			other := strings.Split(rest, "/")[0]
			if other == self || sharedPackages[other] {
				continue
			}
			t.Errorf("%s (%s) imports feature %s: features never import each other; take it in Deps and let root wire it", f, self, other)
		}
	}
}
