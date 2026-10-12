package myapptest

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// The write-site gate: every GORM write in the app's source is run by
// a scenario, or is named below with the reason none needs to. The
// route gate (coverage_test.go) proves every route runs; this proves
// every write inside one does. A write no scenario takes is a write
// whose columns no test has held to its reason's allow-list
// (writes_test.go), so the first to take it would be a person, and
// the hook would refuse them with a 500. That refusal is right (a
// write never lands unexplained); this moves it from production to
// the gate.
//
// How: the census parses every non-test .go file under internal/myapp
// for calls to a GORM write method (writeMethods), each a site
// file:line. The recorder is a GORM callback, before every create,
// update, delete and Exec on the scenario's database, that walks the
// stack to the first frame in that source and marks its line reached.
// TestMain checks the two after m.Run, when every scenario has
// finished. A method of that name on something not GORM is a census
// false positive: name it in notScenarioWrites with that reason.

// notScenarioWrites are write sites no scenario has to run,
// "file.go:line" (relative to internal/myapp) -> reason. Empty today.
// An entry needs a reason a reviewer can check, and an entry for a
// site no longer in the census, or one a scenario now reaches, fails
// the gate.
var notScenarioWrites = map[string]string{}

// writeMethods are the *gorm.DB methods that write.
var writeMethods = []string{
	"Create", "CreateInBatches", "FirstOrCreate", "Save",
	"Update", "Updates", "UpdateColumn", "UpdateColumns",
	"Delete", "Exec",
}

// appSource is the directory the census reads and the recorder
// attributes writes to.
var appSource = func() string {
	dir, err := filepath.Abs(filepath.Join("..", "myapp"))
	if err != nil {
		panic(err)
	}
	return dir
}()

// writeCoverage is the sites scenarios reached, for the whole test
// binary (tests run in parallel: hence the lock).
var writeCoverage = struct {
	sync.Mutex
	reached map[string]bool
	runs    int
}{reached: map[string]bool{}}

// recordWrites registers the recorder on g for a TestScenario* test.
func recordWrites(t *testing.T, g *gorm.DB) {
	t.Helper()
	if !strings.HasPrefix(t.Name(), "TestScenario") {
		return
	}
	writeCoverage.Lock()
	writeCoverage.runs++
	writeCoverage.Unlock()
	mark := func(*gorm.DB) {
		pcs := make([]uintptr, 64)
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
		for {
			f, more := frames.Next()
			if strings.HasPrefix(f.File, appSource+string(filepath.Separator)) && !strings.HasSuffix(f.File, "_test.go") {
				rel, _ := filepath.Rel(appSource, f.File)
				writeCoverage.Lock()
				writeCoverage.reached[fmt.Sprintf("%s:%d", filepath.ToSlash(rel), f.Line)] = true
				writeCoverage.Unlock()
				return
			}
			if !more {
				return
			}
		}
	}
	cb := g.Callback()
	for _, err := range []error{
		cb.Create().Before("gorm:create").Register("writesites:create", mark),
		cb.Update().Before("gorm:update").Register("writesites:update", mark),
		cb.Delete().Before("gorm:delete").Register("writesites:delete", mark),
		cb.Raw().Before("gorm:raw").Register("writesites:raw", mark),
	} {
		if err != nil {
			t.Fatalf("record writes: %v", err)
		}
	}
}

// writeSite is one census entry: where, and in which function.
type writeSite struct {
	at, fn, method string
	lines          []int // the lines a call's frame may report: the method's, and its '(' when on another
}

// writeCensus is every call to a write method in the app's non-test
// source. A call on an imported package's name (os.Create) is not a
// write on the database and is left out.
func writeCensus() ([]writeSite, error) {
	var sites []writeSite
	fset := token.NewFileSet()
	err := filepath.WalkDir(appSource, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		imported := map[string]bool{}
		for _, im := range f.Imports {
			name := strings.Trim(im.Path.Value, `"`)
			name = name[strings.LastIndex(name, "/")+1:]
			if im.Name != nil {
				name = im.Name.Name
			}
			imported[name] = true
		}
		rel, _ := filepath.Rel(appSource, path)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !slices.Contains(writeMethods, sel.Sel.Name) {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && imported[id.Name] {
					return true
				}
				at := fset.Position(sel.Sel.Pos()).Line
				lines := []int{at}
				if l := fset.Position(call.Lparen).Line; l != at {
					lines = append(lines, l)
				}
				sites = append(sites, writeSite{
					at: fmt.Sprintf("%s:%d", filepath.ToSlash(rel), at), fn: fd.Name.Name,
					method: sel.Sel.Name, lines: lines,
				})
				return true
			})
		}
		return nil
	})
	return sites, err
}

// writeGate is TestMain's step after m.Run. It returns the exit code.
// A narrowed run (-run or -skip) has not run every scenario, so it
// cannot say a write is unreached, and checks nothing.
func writeGate() int {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return 0
		}
	}
	sites, err := writeCensus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "writes: census of %s: %v\n", appSource, err)
		return 1
	}
	writeCoverage.Lock()
	defer writeCoverage.Unlock()
	if writeCoverage.runs == 0 || len(sites) == 0 {
		fmt.Fprintln(os.Stderr, "writes: no scenario ran, or the census found no write, so no write can be said to be reached")
		return 1
	}
	file := func(at string) string { return at[:strings.LastIndex(at, ":")] }
	var missing, stale []string
	census := map[string]bool{}
	for _, s := range sites {
		census[s.at] = true
		reached := false
		for _, l := range s.lines {
			reached = reached || writeCoverage.reached[fmt.Sprintf("%s:%d", file(s.at), l)]
		}
		switch {
		case reached && notScenarioWrites[s.at] != "":
			stale = append(stale, s.at+", which a scenario now reaches")
		case !reached && notScenarioWrites[s.at] == "":
			missing = append(missing, fmt.Sprintf("%s (%s in %s)", s.at, s.method, s.fn))
		}
	}
	for at := range notScenarioWrites {
		if !census[at] {
			stale = append(stale, at+", which is no longer a write site")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "writes: no scenario runs the write at internal/myapp/%s; write one, or name it in notScenarioWrites (writesites_test.go) with a reason\n", m)
	}
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "writes: notScenarioWrites names %s; delete the entry\n", s)
	}
	if len(missing)+len(stale) > 0 {
		return 1
	}
	return 0
}
