package myapptest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
)

// The escape-hatch census (pv4's seams ADR, rule 3: every escape hatch
// is counted by a census test). Every way around the change hook is
// found in the source and named below with a reason, so a new one
// fails here by file and line instead of slipping past review:
//
//   - SkipHooks: a GORM session the hook does not see;
//   - Exec or Raw whose SQL names a tracked table: a write the hook
//     cannot read a reason off (the guard trigger still refuses it
//     unless the hook declared it first);
//   - sql.Open outside cmd/: a second connection the guard's TEMP
//     triggers do not exist on;
//   - each TrackedExempt column: a column written with no record.
//
// It parses the non-test source of the app (internal/myapp, cmd/myapp)
// and of libs/, which Go cannot see as test inputs, so it runs under
// its own make target (make escapes) with -count=1.

// escapeRoots are the directories the census reads, by the name a
// hatch's file is reported under.
var escapeRoots = map[string]string{
	"internal/myapp": "../myapp",
	"cmd/myapp":      "../../cmd/myapp",
	"libs":           "../../../../libs",
}

// escapeHatch is one exception: the file, a string on the hatch's line
// that names that site alone, and the reason it is allowed. A string
// that matches two hatches is refused (each is counted on its own),
// and one that matches none is stale.
type escapeHatch struct{ file, at, reason string }

var escapeHatches = []escapeHatch{
	{"internal/myapp/handlers.go", "tx.Session(&gorm.Session{SkipHooks: true})",
		"the password hash's second step: the hook skipped so no diff carries the secret; the guard lets the exempt column alone through"},
	{"internal/myapp/handlers.go", `Exec("UPDATE members SET password_hash = ? WHERE id = ?"`,
		"the same second step, the only raw write on a tracked table; any other column would meet the guard"},
	{"internal/myapp/models.go", "members.password_hash",
		"the hash is a secret: written without a record so no diff carries it (TrackedExempt)"},
	{"libs/changes/changes.go", `Select("request_id").First(&earlier, id)`,
		"the hook reading the cause's request id, a read inside the hook"},
	{"libs/changes/changes.go", "p.Admit(db.Session(",
		"Policy.Admit's session: a check before the write, on the statement's own connection"},
	{"libs/changes/changes.go", "sess := db.Session(",
		"the hook declaring its intent in the TEMP table the guard reads, which is never tracked"},
	{"libs/changes/changes.go", `_ = db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Exec("DELETE FROM changes_intent")`,
		"the hook clearing its intent after the write"},
	{"libs/changes/changes.go", ".Create(&c)",
		"the hook writing the change row itself; changes is append-only and never tracked"},
	{"libs/changes/changes.go", "fn(db.Session(",
		"Trail.OnRecord's ledger write beside the change, in its transaction"},
}

// escape is one hatch the census found.
type escape struct {
	file string // under its root's name: libs/changes/changes.go
	line int
	kind string
	text string // the source line, which an exception's string is found in
}

func (e escape) String() string {
	if e.line == 0 {
		return fmt.Sprintf("%s (%s) %s", e.file, e.kind, e.text)
	}
	return fmt.Sprintf("%s:%d (%s) %s", e.file, e.line, e.kind, e.text)
}

// escapeCensus parses every non-test .go file under roots (name ->
// directory) and returns each hatch in it, plus one per exempt column.
func escapeCensus(roots map[string]string, tracked []string, exempt map[string][]string) ([]escape, error) {
	trackedSQL := regexp.MustCompile(`\b(` + strings.Join(tracked, "|") + `)\b`)
	var found []escape
	fset := token.NewFileSet()
	for name, dir := range roots {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			file := name + "/" + filepath.ToSlash(rel)
			lines := strings.Split(string(src), "\n")
			add := func(n ast.Node, kind string) {
				l := fset.Position(n.Pos()).Line
				found = append(found, escape{file: file, line: l, kind: kind, text: strings.TrimSpace(lines[l-1])})
			}
			sqlPkg := ""
			for _, im := range f.Imports {
				if p, _ := strconv.Unquote(im.Path.Value); p == "database/sql" {
					sqlPkg = "sql"
					if im.Name != nil {
						sqlPkg = im.Name.Name
					}
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.KeyValueExpr:
					if id, ok := n.Key.(*ast.Ident); ok && id.Name == "SkipHooks" {
						add(n, "SkipHooks")
					}
				case *ast.SelectorExpr:
					if n.Sel.Name == "SkipHooks" {
						add(n.Sel, "SkipHooks")
					}
				case *ast.CallExpr:
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					switch {
					case sel.Sel.Name == "Exec" || sel.Sel.Name == "Raw":
						if trackedSQL.MatchString(stringsIn(n.Args)) {
							add(sel.Sel, "raw SQL on a tracked table")
						}
					case sel.Sel.Name == "Open" && sqlPkg != "" && !strings.HasPrefix(file, "cmd/"):
						if id, ok := sel.X.(*ast.Ident); ok && id.Name == sqlPkg {
							add(sel.Sel, "sql.Open outside cmd/")
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for table, cols := range exempt {
		for _, c := range cols {
			found = append(found, escape{file: "internal/myapp/models.go", kind: "TrackedExempt", text: table + "." + c})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line || found[i].line == found[j].line && found[i].text < found[j].text
	})
	return found, nil
}

// stringsIn is every string literal in the call's arguments, joined:
// the SQL, wherever the call builds it from.
func stringsIn(args []ast.Expr) string {
	var b strings.Builder
	for _, a := range args {
		ast.Inspect(a, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				b.WriteString(s + " ")
			}
			return true
		})
	}
	return b.String()
}

// judgeEscapes is every hatch no exception names, every exception
// that names none (stale) and every one that names more than one.
func judgeEscapes(found []escape, hatches []escapeHatch) []string {
	var problems []string
	named := make([]int, len(found))
	for _, h := range hatches {
		var hits []string
		for i, e := range found {
			if e.file == h.file && strings.Contains(e.text, h.at) {
				named[i]++
				hits = append(hits, e.String())
			}
		}
		switch {
		case len(hits) == 0:
			problems = append(problems, fmt.Sprintf("stale exception: %s %q names no escape hatch; delete it", h.file, h.at))
		case len(hits) > 1:
			problems = append(problems, fmt.Sprintf("exception %s %q names %d hatches (%s); give each its own string", h.file, h.at, len(hits), strings.Join(hits, "; ")))
		}
	}
	for i, e := range found {
		if named[i] == 0 {
			problems = append(problems, fmt.Sprintf("escape hatch %s is named by no exception: remove it, or name it in escapeHatches (escapes_test.go) with a reason", e))
		}
	}
	return problems
}

func TestEveryEscapeHatchIsNamed(t *testing.T) {
	t.Parallel()
	found, err := escapeCensus(escapeRoots, myapp.Tracked, myapp.TrackedExempt)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range judgeEscapes(found, escapeHatches) {
		t.Error(p)
	}
}

// The census can see: a planted file with one of each hatch is found,
// each by kind, and judged against the exceptions it is red on the
// unnamed hatch and on the stale exception.
func TestTheEscapeCensusSeesPlantedHatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	planted := `package planted

import (
	"database/sql"

	"gorm.io/gorm"
)

func hatches(g *gorm.DB) {
	g.Session(&gorm.Session{SkipHooks: true}).Create(nil)
	g.Exec("DELETE FROM notes WHERE id = ?", 1)
	g.Raw("SELECT name FROM pragma_table_info(?)", "x")
	sql.Open("sqlite", "x.db")
}
`
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := escapeCensus(map[string]string{"libs": dir}, myapp.Tracked, map[string][]string{"members": {"password_hash"}})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range found {
		kinds = append(kinds, e.kind)
	}
	want := []string{"TrackedExempt", "SkipHooks", "raw SQL on a tracked table", "sql.Open outside cmd/"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("census kinds = %v, want %v (the Raw naming no tracked table is no hatch)", kinds, want)
	}

	problems := judgeEscapes(found, []escapeHatch{
		{"internal/myapp/models.go", "members.password_hash", "planted"},
		{"libs/planted.go", "SkipHooks: true", "planted"},
		{"libs/planted.go", `sql.Open("sqlite"`, "planted"},
		{"libs/planted.go", "gone()", "planted"},
	})
	got := strings.Join(problems, "\n")
	if len(problems) != 2 || !strings.Contains(got, "libs/planted.go:11 (raw SQL on a tracked table)") || !strings.Contains(got, `stale exception: libs/planted.go "gone()"`) {
		t.Fatalf("judged the planted file:\n%s\nwant the unnamed Exec and the stale exception, nothing else", got)
	}
	if p := judgeEscapes(found, []escapeHatch{{"libs/planted.go", "g.", "too broad"}}); !strings.Contains(strings.Join(p, "\n"), `names 2 hatches`) {
		t.Fatalf("an exception naming two hatches was not refused: %v", p)
	}
}
