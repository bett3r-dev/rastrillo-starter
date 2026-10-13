package myapptest

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"amadan.net/rastrillo/rastrillo/db"
	gormschema "gorm.io/gorm/schema"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
)

// The flow chain, read off the change records the scenarios land:
//
//	Command → [context] Event  ⟹  [other context] Event
//
// The Command is the change's entry point; the context is the one
// that declared its reason; a policy is a change in one context whose
// cause names a change in another. This file only extracts the chain
// and reports what it finds: a change with no entry point, an entry
// point opened with no name (the hook recorded its route), a request
// landing directly in two contexts and a change outside its entry
// point's context that no policy reaches each get a heading in
// docs/flow.md and a list in docs/flow.json. None of them fails
// anything.
//
// The map also carries what the app declares (its invariants, what
// each reason writes) and where every name it holds is declared in the
// source (flowBuild).
//
// Two things fail: drift, and a name the map carries that the source
// scan finds nowhere (census). The extraction needs every scenario, so
// it runs only when asked, after m.Run: make flow (FLOW_UPDATE=1)
// writes docs/flow.md and docs/flow.json, make flow-check
// (FLOW_CHECK=1, in make ci) fails when either committed file is not
// what the scenarios produce. A plain go test collects nothing and
// checks nothing.

const (
	flowDoc  = "../../docs/flow.md"
	flowJSON = "../../docs/flow.json"
)

func flowWanted() bool { return os.Getenv("FLOW_CHECK") != "" || os.Getenv("FLOW_UPDATE") != "" }

// landed is every change each scenario landed, by scenario name.
var landed = struct {
	sync.Mutex
	by map[string][]changes.Change
}{by: map[string][]changes.Change{}}

// collectFlow reads the scenario's whole change record when it ends.
// Registered after newApp's Close, so it runs before it.
func collectFlow(t *testing.T, d *db.DB) {
	if !flowWanted() || !strings.HasPrefix(t.Name(), "TestScenario") {
		return
	}
	t.Cleanup(func() {
		var cs []changes.Change
		if err := d.G.Order("id").Find(&cs).Error; err != nil {
			t.Errorf("flow: read changes: %v", err)
			return
		}
		landed.Lock()
		landed.by[t.Name()] = cs
		landed.Unlock()
	})
}

// flowGate is TestMain's step after m.Run. It returns the exit code.
func flowGate() int {
	if !flowWanted() {
		return 0
	}
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		fmt.Fprintln(os.Stderr, "flow: the chain needs every scenario; run make flow or make flow-check, not -run")
		return 1
	}
	m, err := flowBuild(landed.by, appRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flow:", err)
		return 1
	}
	files := map[string][]byte{flowDoc: m.markdown(), flowJSON: m.json()}
	for _, path := range []string{flowDoc, flowJSON} {
		if os.Getenv("FLOW_UPDATE") != "" {
			if err := os.WriteFile(path, files[path], 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "flow:", err)
				return 1
			}
			continue
		}
		if committed, err := os.ReadFile(path); err != nil || !bytes.Equal(committed, files[path]) {
			fmt.Fprintln(os.Stderr, "flow: docs/flow.md and docs/flow.json are not what the scenarios produce. Run `make flow` and commit both.")
			return 1
		}
	}
	return 0
}

// unnamed is an entry point the hook filled in from the route
// ("POST /members/{id}/leave") because the Trail was opened without a
// name.
func unnamed(ep string) bool { return strings.ContainsAny(ep, " /") }

// The map, as docs/flow.json has it. Field order and names are the
// file's shape: a board renders from it, so change them only on
// purpose, and bump flowVersion when you do. Every list is sorted and
// never null.
const flowVersion = 2

// flowApp is the app the map is of, and appRoot its root from this
// package: every source is a file relative to it.
const (
	flowApp = "myapp"
	appRoot = "../.."
)

type flowMap struct {
	Version           int             `json:"version"`
	App               string          `json:"app"`
	EntryPoints       []flowEntry     `json:"entry_points"`
	Unnamed           []flowEntry     `json:"unnamed"`
	Policies          []flowPolicy    `json:"policies"`
	Chains            []flowChain     `json:"chains"`
	Empty             []flowEmpty     `json:"empty"`
	Mixed             []flowMixed     `json:"mixed"`
	ImplicitCrossings []flowCrossing  `json:"implicit_crossings"`
	Invariants        []flowInvariant `json:"invariants"`
	Writes            []flowWrites    `json:"writes"`
	Sources           flowSources     `json:"sources"`
}

// flowEntry is what one entry point lands directly in one context.
// Actors are who opened it: "member", "platform" (a platform admin
// acting as a member, blueprint2's ADR 0005), "human" or "agent:<name>".
type flowEntry struct {
	Name    string     `json:"name"`
	Context string     `json:"context"`
	Reasons []string   `json:"reasons"`
	Actors  []string   `json:"actors"`
	Rules   []flowRule `json:"rules"`
}

// flowRule is a reason landed under a named rule (Trail.By): the
// change record's rule.
type flowRule struct {
	Reason string `json:"reason"`
	Rule   string `json:"rule"`
}

type flowEvent struct {
	Reason  string `json:"reason"`
	Context string `json:"context"`
}

func (e flowEvent) String() string { return e.Reason + "[" + e.Context + "]" }

type flowPolicy struct {
	From      flowEvent  `json:"from"`
	To        flowEvent  `json:"to"`
	Scenarios []string   `json:"scenarios"`
	Rules     []flowRule `json:"rules"`
}

// flowInvariant is one declared rule (myapp.Invariants): the reasons
// it guards (may refuse) and shapes (reason -> tables), and where it
// is declared.
type flowInvariant struct {
	Name    string              `json:"name"`
	Context string              `json:"context"`
	Rule    string              `json:"rule"`
	Guards  []string            `json:"guards"`
	Shapes  map[string][]string `json:"shapes"`
	Source  flowSource          `json:"source"`
}

// flowWrites is the tables one reason may write (myapp.ReasonWrites,
// tables only).
type flowWrites struct {
	Reason  string   `json:"reason"`
	Context string   `json:"context"`
	Tables  []string `json:"tables"`
}

// flowSource is where a name is declared: a file relative to the
// app's root, and its line.
type flowSource struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// flowSources is each name the map carries, by kind, to its source.
type flowSources struct {
	EntryPoints map[string]flowSource `json:"entry_points"`
	Reasons     map[string]flowSource `json:"reasons"`
	Tables      map[string]flowSource `json:"tables"`
	Invariants  map[string]flowSource `json:"invariants"`
}

// flowChain is one path: an entry point, a fact it landed directly,
// then each cross-context policy that fact set off, in order.
// Same-context follow-ups are left out.
type flowChain struct {
	Line       string       `json:"line"`
	EntryPoint string       `json:"entry_point"`
	Event      flowEvent    `json:"event"`
	Then       []flowPolicy `json:"then"`
	Scenarios  []string     `json:"scenarios"`
}

type flowEmpty struct {
	Scenario string    `json:"scenario"`
	ChangeID int64     `json:"change_id"`
	Event    flowEvent `json:"event"`
}

type flowMixed struct {
	Scenario   string      `json:"scenario"`
	EntryPoint string      `json:"entry_point"`
	Contexts   []flowEntry `json:"contexts"`
}

type flowCrossing struct {
	Scenario   string    `json:"scenario"`
	ChangeID   int64     `json:"change_id"`
	Event      flowEvent `json:"event"`
	EntryPoint string    `json:"entry_point"`
	Home       string    `json:"home"`
}

func eventOf(c changes.Change) flowEvent {
	return flowEvent{Reason: c.Reason, Context: myapp.ContextOfReason(c.Reason)}
}

// actorKind drops the member id: a board shows who, not which one.
func actorKind(a string) string {
	// A member or a platform admin is a kind of person, never one
	// person: the map names who opens an entry point, not which id.
	for _, kind := range []string{"member", "platform"} {
		if strings.HasPrefix(a, kind+":") {
			return kind
		}
	}
	// An agent acting for a member names the member too
	// (agent:claude-code:<id>, blueprint2's ADR 0007): the map names the agent.
	if name, ok := strings.CutPrefix(a, "agent:"); ok {
		if kind, _, ok := strings.Cut(name, ":"); ok {
			return "agent:" + kind
		}
	}
	return a
}

// flowOf builds the map from the scenarios' changes.
func flowOf(by map[string][]changes.Change) flowMap {
	type set = map[string]bool
	direct := map[string]map[string]set{} // entry point -> context -> reason
	actors := map[string]set{}            // entry point -> actor kind
	rules := map[string]map[string]set{}  // entry point -> context -> rule key
	policies := map[[2]flowEvent]set{}    // -> scenarios
	hopRules := map[[2]flowEvent]set{}    // -> rule key
	chains := map[string]*flowChain{}     // by line
	m := flowMap{Version: flowVersion, App: flowApp}

	for _, scen := range sortedKeys(by) {
		cs := by[scen]
		byID := map[int64]changes.Change{}
		children := map[int64][]changes.Change{}
		for _, c := range cs {
			byID[c.ID] = c
		}
		for _, c := range cs {
			if p, ok := parentOf(c, byID); ok {
				children[p.ID] = append(children[p.ID], c)
			}
		}
		// One request, one entry point: the contexts it landed in directly.
		requests := map[string]map[string][]string{}
		// One request, entry point and fact: the policy paths below it.
		roots := map[string]*flowChain{}
		paths := map[string][][]flowPolicy{}
		for _, c := range cs {
			ev := eventOf(c)
			if c.EntryPoint == "" {
				m.Empty = append(m.Empty, flowEmpty{Scenario: scen, ChangeID: c.ID, Event: ev})
			}
			// A cause from an earlier request (a correction, an agent
			// fulfilling a request) is where this request starts, as a
			// request cause is: the change is its entry point's own.
			if strings.HasPrefix(c.Cause, "change:") && !causedEarlier(c, byID) {
				p, ok := parentOf(c, byID)
				if pev := eventOf(p); ok && pev.Context != ev.Context {
					a := [2]flowEvent{pev, ev}
					if policies[a] == nil {
						policies[a], hopRules[a] = set{}, set{}
					}
					policies[a][scen] = true
					if c.Rule != "" {
						hopRules[a][ruleKey(c)] = true
					}
				}
				continue
			}
			if c.EntryPoint == "" {
				continue
			}
			if direct[c.EntryPoint] == nil {
				direct[c.EntryPoint] = map[string]set{}
				actors[c.EntryPoint] = set{}
			}
			if direct[c.EntryPoint][ev.Context] == nil {
				direct[c.EntryPoint][ev.Context] = set{}
			}
			direct[c.EntryPoint][ev.Context][c.Reason] = true
			if c.Rule != "" {
				if rules[c.EntryPoint] == nil {
					rules[c.EntryPoint] = map[string]set{}
				}
				if rules[c.EntryPoint][ev.Context] == nil {
					rules[c.EntryPoint][ev.Context] = set{}
				}
				rules[c.EntryPoint][ev.Context][ruleKey(c)] = true
			}
			actors[c.EntryPoint][actorKind(c.Actor)] = true
			key := c.CorrelationID + "\x00" + c.EntryPoint
			if requests[key] == nil {
				requests[key] = map[string][]string{}
			}
			requests[key][ev.Context] = append(requests[key][ev.Context], c.Reason)
			g := key + "\x00" + c.Reason
			if roots[g] == nil {
				roots[g] = &flowChain{EntryPoint: c.EntryPoint, Event: ev}
			}
			paths[g] = append(paths[g], crossingsBelow(c, children)...)
		}
		// A request's fact counts once, by the longest lines it set
		// off: a row of it that crossed nowhere does not add a bare one.
		for _, g := range sortedKeys(paths) {
			for _, path := range longest(paths[g]) {
				r := roots[g]
				line := r.EntryPoint + " → [" + r.Event.Context + "] " + r.Event.Reason
				for _, p := range path {
					line += " ⟹ [" + p.To.Context + "] " + p.To.Reason
				}
				if chains[line] == nil {
					chains[line] = &flowChain{Line: line, EntryPoint: r.EntryPoint, Event: r.Event, Then: path}
				}
				chains[line].Scenarios = append(chains[line].Scenarios, scen)
			}
		}
		for _, key := range sortedKeys(requests) {
			ctxs := requests[key]
			if len(ctxs) < 2 {
				continue
			}
			_, ep, _ := strings.Cut(key, "\x00")
			mx := flowMixed{Scenario: scen, EntryPoint: ep}
			for _, ctx := range sortedKeys(ctxs) {
				mx.Contexts = append(mx.Contexts, flowEntry{Name: ep, Context: ctx, Reasons: uniq(ctxs[ctx]), Actors: []string{}, Rules: []flowRule{}})
			}
			m.Mixed = append(m.Mixed, mx)
		}
	}

	// An entry point's home is the one context it lands in directly.
	// A change elsewhere under it should be reached by a policy edge on
	// its way up the causes; one that is not is an implicit crossing.
	home := map[string]string{}
	for _, ep := range sortedKeys(direct) {
		if ctxs := sortedKeys(direct[ep]); len(ctxs) == 1 {
			home[ep] = ctxs[0]
		}
	}
	for _, scen := range sortedKeys(by) {
		byID := map[int64]changes.Change{}
		for _, c := range by[scen] {
			byID[c.ID] = c
		}
		for _, c := range by[scen] {
			want, ok := home[c.EntryPoint]
			ev := eventOf(c)
			if !ok || ev.Context == want {
				continue
			}
			seen := map[int64]bool{}
			for cur := c; ; {
				seen[cur.ID] = true
				p, found := parentOf(cur, byID)
				if !found || seen[p.ID] {
					m.ImplicitCrossings = append(m.ImplicitCrossings, flowCrossing{Scenario: scen, ChangeID: c.ID, Event: ev, EntryPoint: c.EntryPoint, Home: want})
					break
				}
				if myapp.ContextOfReason(p.Reason) != ev.Context {
					break
				}
				cur = p
			}
		}
	}

	m.EntryPoints, m.Unnamed = []flowEntry{}, []flowEntry{}
	for _, ep := range sortedKeys(direct) {
		for _, ctx := range sortedKeys(direct[ep]) {
			e := flowEntry{Name: ep, Context: ctx, Reasons: sortedKeys(direct[ep][ctx]), Actors: sortedKeys(actors[ep]), Rules: rulesOf(rules[ep][ctx])}
			if unnamed(ep) {
				m.Unnamed = append(m.Unnamed, e)
			} else {
				m.EntryPoints = append(m.EntryPoints, e)
			}
		}
	}
	m.Policies = []flowPolicy{}
	for a, scens := range policies {
		m.Policies = append(m.Policies, flowPolicy{From: a[0], To: a[1], Scenarios: sortedKeys(scens), Rules: rulesOf(hopRules[a])})
	}
	sort.Slice(m.Policies, func(i, j int) bool {
		pi, pj := m.Policies[i], m.Policies[j]
		return pi.From.String()+pi.To.String() < pj.From.String()+pj.To.String()
	})
	m.Chains = []flowChain{}
	for _, line := range sortedKeys(chains) {
		ch := *chains[line]
		ch.Scenarios = uniq(ch.Scenarios)
		sort.Strings(ch.Scenarios)
		m.Chains = append(m.Chains, ch)
	}
	if m.Empty == nil {
		m.Empty = []flowEmpty{}
	}
	if m.Mixed == nil {
		m.Mixed = []flowMixed{}
	}
	if m.ImplicitCrossings == nil {
		m.ImplicitCrossings = []flowCrossing{}
	}
	// Two requests of one entry point in one scenario are keyed by their
	// trace ids, which are random: what they landed breaks the tie, so
	// the map does not change from run to run.
	mixedKey := func(mx flowMixed) string {
		k := mx.EntryPoint + "\x00" + mx.Scenario
		for _, c := range mx.Contexts {
			k += "\x00" + c.Context + ":" + strings.Join(c.Reasons, ",")
		}
		return k
	}
	sort.SliceStable(m.Mixed, func(i, j int) bool { return mixedKey(m.Mixed[i]) < mixedKey(m.Mixed[j]) })

	// What the app declares, whatever the scenarios landed.
	m.Invariants = []flowInvariant{}
	for _, inv := range myapp.Invariants {
		fi := flowInvariant{Name: inv.Name, Context: inv.Context, Rule: inv.Rule,
			Guards: append([]string{}, inv.Guards...), Shapes: map[string][]string{}}
		sort.Strings(fi.Guards)
		for reason, tables := range inv.Shapes {
			fi.Shapes[reason] = append([]string{}, tables...)
			sort.Strings(fi.Shapes[reason])
		}
		m.Invariants = append(m.Invariants, fi)
	}
	sort.Slice(m.Invariants, func(i, j int) bool { return m.Invariants[i].Name < m.Invariants[j].Name })
	m.Writes = []flowWrites{}
	for _, reason := range sortedKeys(myapp.ReasonWrites) {
		m.Writes = append(m.Writes, flowWrites{Reason: reason, Context: myapp.ContextOfReason(reason), Tables: sortedKeys(myapp.ReasonWrites[reason])})
	}
	m.Sources = flowSources{EntryPoints: map[string]flowSource{}, Reasons: map[string]flowSource{},
		Tables: map[string]flowSource{}, Invariants: map[string]flowSource{}}
	return m
}

// ruleKey and rulesOf keep a (reason, rule) pair in a set, and read
// the set back as the sorted list flow.json carries.
func ruleKey(c changes.Change) string { return c.Reason + "\x00" + c.Rule }

func rulesOf(keys map[string]bool) []flowRule {
	out := []flowRule{}
	for _, k := range sortedKeys(keys) {
		reason, rule, _ := strings.Cut(k, "\x00")
		out = append(out, flowRule{Reason: reason, Rule: rule})
	}
	return out
}

// flowBuild is the map flowGate writes and checks: flowOf, with every
// name located in the app's source under root. A name with no source
// is an error naming it (census).
func flowBuild(by map[string][]changes.Change, root string) (flowMap, error) {
	m := flowOf(by)
	if err := m.locate(root); err != nil {
		return m, err
	}
	if missing := m.census(); len(missing) > 0 {
		return m, errors.New(strings.Join(missing, "; "))
	}
	return m, nil
}

// locate fills in the sources from the app's non-test Go files under
// root, read with go/parser. An entry point's is the first string
// literal equal to its name that a call takes as an argument or a
// field named EntryPoint is assigned (createMember names Join that way),
// files in path order; a reason's and an invariant's is the constant
// declaring it in reasons_<context>.go; a table's is its model's
// struct in models.go. A name found nowhere is left out, for census.
// It reads files Go's test cache does not see: make flow and make
// flow-check run with -count=1.
func (m *flowMap) locate(root string) error {
	want := m.names()
	tableOf := map[string]string{} // model struct -> table
	for _, model := range myapp.Models {
		sc, err := gormschema.Parse(model, &sync.Map{}, gormschema.NamingStrategy{})
		if err != nil {
			return err
		}
		tableOf[sc.Name] = sc.Table
	}
	fset := token.NewFileSet()
	at := func(rel string, pos token.Pos) flowSource {
		return flowSource{File: rel, Line: fset.Position(pos).Line}
	}
	first := func(into map[string]flowSource, names map[string]bool, name string, src flowSource) {
		if _, seen := into[name]; names[name] && !seen {
			into[name] = src
		}
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		base := filepath.Base(path)
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				for _, arg := range n.Args {
					if lit, ok := stringLit(arg); ok {
						first(m.Sources.EntryPoints, want.entryPoints, lit, at(rel, arg.Pos()))
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "EntryPoint" && i < len(n.Rhs) {
						if lit, ok := stringLit(n.Rhs[i]); ok {
							first(m.Sources.EntryPoints, want.entryPoints, lit, at(rel, n.Rhs[i].Pos()))
						}
					}
				}
			case *ast.ValueSpec:
				if !strings.HasPrefix(base, "reasons_") {
					return true
				}
				for i, name := range n.Names {
					if i >= len(n.Values) {
						break
					}
					if lit, ok := stringLit(n.Values[i]); ok {
						first(m.Sources.Reasons, want.reasons, lit, at(rel, name.Pos()))
						first(m.Sources.Invariants, want.invariants, lit, at(rel, name.Pos()))
					}
				}
			case *ast.TypeSpec:
				if _, isStruct := n.Type.(*ast.StructType); isStruct && base == "models.go" {
					first(m.Sources.Tables, want.tables, tableOf[n.Name.Name], at(rel, n.Name.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return err
	}
	for i, inv := range m.Invariants {
		m.Invariants[i].Source = m.Sources.Invariants[inv.Name]
	}
	return nil
}

// flowNames is every name the map carries, by kind: the named entry
// points, every declared reason and invariant, and every table a
// reason writes.
type flowNames struct{ entryPoints, reasons, tables, invariants map[string]bool }

func (m flowMap) names() flowNames {
	n := flowNames{map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}}
	for _, e := range m.EntryPoints {
		n.entryPoints[e.Name] = true
	}
	for _, r := range myapp.Reasons {
		n.reasons[r] = true
	}
	for _, w := range m.Writes {
		for _, t := range w.Tables {
			n.tables[t] = true
		}
	}
	for _, inv := range m.Invariants {
		n.invariants[inv.Name] = true
	}
	return n
}

// census is every name the map carries that has no source, each named
// with its kind.
func (m flowMap) census() []string {
	n := m.names()
	var missing []string
	for _, k := range []struct {
		kind  string
		names map[string]bool
		found map[string]flowSource
	}{
		{"entry point", n.entryPoints, m.Sources.EntryPoints},
		{"reason", n.reasons, m.Sources.Reasons},
		{"table", n.tables, m.Sources.Tables},
		{"invariant", n.invariants, m.Sources.Invariants},
	} {
		for _, name := range sortedKeys(k.names) {
			if _, ok := k.found[name]; !ok {
				missing = append(missing, k.kind+" "+name+" has no source")
			}
		}
	}
	return missing
}

// stringLit is e's value when e is a string literal.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// crossingsBelow is every path of cross-context policies under the
// change c, following its descendants and keeping only the hops that
// leave a context, one path per leaf. A change that sets off no
// crossing has the one empty path.
func crossingsBelow(c changes.Change, children map[int64][]changes.Change) [][]flowPolicy {
	var out [][]flowPolicy
	var walk func(cur changes.Change, path []flowPolicy, seen map[int64]bool)
	walk = func(cur changes.Change, path []flowPolicy, seen map[int64]bool) {
		seen[cur.ID] = true
		leaf := true
		for _, ch := range children[cur.ID] {
			if seen[ch.ID] {
				continue
			}
			leaf = false
			next := path
			if from, to := eventOf(cur), eventOf(ch); from.Context != to.Context {
				hop := flowPolicy{From: from, To: to, Scenarios: []string{}, Rules: []flowRule{}}
				if ch.Rule != "" {
					hop.Rules = []flowRule{{Reason: ch.Reason, Rule: ch.Rule}}
				}
				next = append(append([]flowPolicy{}, path...), hop)
			}
			walk(ch, next, seen)
		}
		if leaf {
			out = append(out, path)
		}
	}
	walk(c, []flowPolicy{}, map[int64]bool{})
	return out
}

// longest drops repeated paths and every path that is the start of a
// longer one.
func longest(out [][]flowPolicy) [][]flowPolicy {
	key := func(p []flowPolicy) string {
		var b strings.Builder
		for _, h := range p {
			b.WriteString(h.From.String() + ">" + h.To.String() + ";")
		}
		return b.String()
	}
	keep := map[string][]flowPolicy{}
	for _, p := range out {
		keep[key(p)] = p
	}
	var paths [][]flowPolicy
	for _, k := range sortedKeys(keep) {
		prefix := false
		for other := range keep {
			if other != k && strings.HasPrefix(other, k) {
				prefix = true
			}
		}
		if !prefix {
			paths = append(paths, keep[k])
		}
	}
	return paths
}

func (m flowMap) json() []byte {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err) // plain structs of strings and ints
	}
	return append(b, '\n')
}

func (m flowMap) markdown() []byte {
	var b bytes.Buffer
	b.WriteString("<!-- Generated by make flow from the change records the scenarios in internal/myapptest land, with docs/flow.json beside it. Never edit by hand: make flow-check (in make ci) fails when this file is not what the scenarios produce. -->\n\n")
	b.WriteString("# The flow chain\n\n")
	b.WriteString("    Command → [context] Event  ⟹  [other context] Event\n\n")
	b.WriteString("Extracted, not enforced: this is the map of what the code does, and nothing here fails on what it finds.\n\n")
	entryTable := func(es []flowEntry) {
		if len(es) == 0 {
			b.WriteString("None.\n")
			return
		}
		b.WriteString("| Entry point | Context | Reasons | Actors |\n|---|---|---|---|\n")
		for _, e := range es {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", e.Name, e.Context, strings.Join(e.Reasons, ", "), strings.Join(e.Actors, ", "))
		}
	}
	b.WriteString("## Entry points\n\nWhat each named entry point lands directly: caused by the request, or by a change an earlier request landed, not by another change of its own.\n\n")
	entryTable(m.EntryPoints)
	b.WriteString("\n## Unnamed entry points\n\nTrails opened with no name: the hook recorded the request's method and route instead.\n\n")
	entryTable(m.Unnamed)
	b.WriteString("\n## Policies\n\nA change in one context caused by a change in another, with the scenarios that land it.\n\n")
	if len(m.Policies) == 0 {
		b.WriteString("None.\n")
	}
	for _, p := range m.Policies {
		fmt.Fprintf(&b, "- `%s ⟹ %s`: %s\n", p.From, p.To, strings.Join(p.Scenarios, ", "))
	}
	b.WriteString("\n## Chains\n\nEach entry point's direct facts, followed through the cross-context policies they set off, with the scenarios that land the whole line. Same-context follow-ups are left out.\n\n")
	if len(m.Chains) == 0 {
		b.WriteString("None.\n")
	}
	for _, c := range m.Chains {
		fmt.Fprintf(&b, "- `%s`: %s\n", c.Line, strings.Join(c.Scenarios, ", "))
	}
	b.WriteString("\n## Rules\n\nEach reason with the rules its changes were landed under (Trail.By), off the entry points and the policies.\n\n")
	ruled := map[string]map[string]bool{}
	for _, rs := range slices.Concat(entryRules(m.EntryPoints), entryRules(m.Unnamed), policyRules(m.Policies)) {
		if ruled[rs.Reason] == nil {
			ruled[rs.Reason] = map[string]bool{}
		}
		ruled[rs.Reason][rs.Rule] = true
	}
	if len(ruled) == 0 {
		b.WriteString("None.\n")
	}
	for _, reason := range sortedKeys(ruled) {
		fmt.Fprintf(&b, "- `%s`: %s\n", reason, strings.Join(sortedKeys(ruled[reason]), ", "))
	}
	list := func(heading, intro string, items []string) {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n\n", heading, intro)
		if len(items) == 0 {
			b.WriteString("None.\n")
		}
		for _, it := range items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	var empty, mixed, crossings []string
	for _, e := range m.Empty {
		empty = append(empty, fmt.Sprintf("%s: change #%d (%s)", e.Scenario, e.ChangeID, e.Event))
	}
	for _, mx := range m.Mixed {
		var parts []string
		for _, c := range mx.Contexts {
			parts = append(parts, c.Context+": "+strings.Join(c.Reasons, ", "))
		}
		mixed = append(mixed, fmt.Sprintf("`%s` in %s lands directly in %s", mx.EntryPoint, mx.Scenario, strings.Join(parts, "; ")))
	}
	for _, c := range m.ImplicitCrossings {
		crossings = append(crossings, fmt.Sprintf("%s: %s under `%s` [%s], change #%d", c.Scenario, c.Event, c.EntryPoint, c.Home, c.ChangeID))
	}
	list("Empty entry points", "Changes that carry no entry point at all: written outside any routed request by a Trail with no name.", empty)
	list("Mixed-context requests", "One request landing directly in two contexts, neither change caused by the other.", mixed)
	list("Implicit crossings", "A change outside its entry point's context that no policy reaches: no cause on its way up comes from another context.", crossings)
	return b.Bytes()
}

func entryRules(es []flowEntry) []flowRule {
	var out []flowRule
	for _, e := range es {
		out = append(out, e.Rules...)
	}
	return out
}

func policyRules(ps []flowPolicy) []flowRule {
	var out []flowRule
	for _, p := range ps {
		out = append(out, p.Rules...)
	}
	return out
}

// parentOf is the change c's cause names, when it names one landed
// by the same request: a cause counts as a parent only within one
// request (ADR 0003, the same-request parent rule).
func parentOf(c changes.Change, byID map[int64]changes.Change) (changes.Change, bool) {
	p, ok := causeOf(c, byID)
	if !ok || p.CorrelationID != c.CorrelationID {
		return changes.Change{}, false
	}
	return p, true
}

// causedEarlier says c's cause is a change an earlier request landed.
func causedEarlier(c changes.Change, byID map[int64]changes.Change) bool {
	p, ok := causeOf(c, byID)
	return ok && p.CorrelationID != c.CorrelationID
}

// causeOf is the change c's cause names, from any request.
func causeOf(c changes.Change, byID map[int64]changes.Change) (changes.Change, bool) {
	rest, ok := strings.CutPrefix(c.Cause, "change:")
	if !ok {
		return changes.Change{}, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return changes.Change{}, false
	}
	p, ok := byID[id]
	return p, ok
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The extraction reports, under their own headings, what an earlier
// arm of the gastos benchmark failed on: an empty entry point, a
// route for an entry point, a request landing directly in both
// contexts, and a change in another context that no policy reaches.
func TestFlowReportsWhatItFinds(t *testing.T) {
	t.Parallel()
	direct := func(id int64, ep, reason string) changes.Change {
		return changes.Change{ID: id, EntryPoint: ep, Reason: reason, Cause: "request:r", CorrelationID: "r", Actor: "member:1"}
	}
	found := flowOf(map[string][]changes.Change{
		"TestScenarioX": {
			direct(1, "", myapp.ReasonNoteWritten),
			direct(2, "POST /notes/{id}/archive", myapp.ReasonNoteCorrected),
			direct(3, "Join", myapp.ReasonMemberJoined),
			direct(4, "Join", myapp.ReasonWelcomeNoteWritten),
		},
	})
	// Every list and map is [] or {} in flow.json, findings included.
	if j := string(found.json()); strings.Contains(j, "null") {
		t.Errorf("a null in flow.json:\n%s", j)
	}
	doc := string(found.markdown())
	for _, want := range []string{
		"## Rules\n\nEach reason with the rules its changes were landed under (Trail.By), off the entry points and the policies.\n\nNone.\n",
		"## Empty entry points\n\nChanges that carry no entry point at all: written outside any routed request by a Trail with no name.\n\n- TestScenarioX: change #1",
		"| POST /notes/{id}/archive | notes | NoteCorrected | member |",
		"- `Join` in TestScenarioX lands directly in accounts: MemberJoined; notes: WelcomeNoteWritten",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in\n%s", want, doc)
		}
	}
	if named, _, _ := strings.Cut(doc, "## Unnamed entry points"); strings.Contains(named, "POST /notes/{id}/archive") {
		t.Errorf("an unnamed entry point is listed with the named ones:\n%s", doc)
	}
	// WriteNote's home is notes; an accounts change under it whose
	// causes never leave accounts is reached by no policy.
	doc = string(flowOf(map[string][]changes.Change{"TestScenarioZ": {
		{ID: 1, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:r", CorrelationID: "r"},
		{ID: 2, EntryPoint: "WriteNote", Reason: myapp.ReasonMemberJoined, Cause: "change:3", CorrelationID: "r"},
		{ID: 3, EntryPoint: "WriteNote", Reason: myapp.ReasonMemberJoined, Cause: "change:99", CorrelationID: "r"},
	}}).markdown())
	if !strings.Contains(doc, "- TestScenarioZ: MemberJoined[accounts] under `WriteNote` [notes], change #2") {
		t.Errorf("implicit crossing not reported:\n%s", doc)
	}
	// A cause from an earlier request is where this request starts:
	// CorrectNote lands its fact directly, and no policy edge comes of it.
	doc = string(flowOf(map[string][]changes.Change{"TestScenarioE": {
		{ID: 1, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:r", CorrelationID: "r", Actor: "member:1"},
		{ID: 2, EntryPoint: "CorrectNote", Reason: myapp.ReasonNoteCorrected, Cause: "change:1", CorrelationID: "s", Actor: "member:1"},
	}}).markdown())
	if !strings.Contains(doc, "| CorrectNote | notes | "+myapp.ReasonNoteCorrected+" | member |") {
		t.Errorf("a change caused by an earlier request is not its entry point's own:\n%s", doc)
	}
}

// A chain follows a direct fact through every crossing below it,
// skipping same-context hops, and a fact with no crossing is a chain
// of its own.
func TestFlowComposesChains(t *testing.T) {
	t.Parallel()
	m := flowOf(map[string][]changes.Change{"TestScenarioC": {
		{ID: 1, EntryPoint: "Join", Reason: myapp.ReasonMemberJoined, Cause: "request:r", CorrelationID: "r"},
		{ID: 2, EntryPoint: "Join", Reason: myapp.ReasonMemberJoined, Cause: "change:1", CorrelationID: "r"},
		{ID: 3, EntryPoint: "Join", Reason: myapp.ReasonWelcomeNoteWritten, Cause: "change:2", CorrelationID: "r"},
		{ID: 4, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:s", CorrelationID: "s"},
	}})
	var lines []string
	for _, c := range m.Chains {
		lines = append(lines, c.Line)
	}
	want := []string{
		"Join → [accounts] MemberJoined ⟹ [notes] WelcomeNoteWritten",
		"WriteNote → [notes] NoteWritten",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("chains = %q, want %q", lines, want)
	}
	if len(m.Chains[0].Then) != 1 || m.Chains[0].Then[0].From.Reason != myapp.ReasonMemberJoined {
		t.Errorf("the crossing's own from is lost: %+v", m.Chains[0].Then)
	}
	if !strings.Contains(string(m.json()), `"empty": []`) {
		t.Errorf("an empty list is not [] in flow.json:\n%s", m.json())
	}
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// A hop's rule is the record's: the welcome note carries the rule that
// shaped it, so the policy that writes it says so, and so does an
// entry point's own fact. A list with no rule is [], never null.
func TestFlowCarriesTheRulesOffTheRecords(t *testing.T) {
	t.Parallel()
	m := flowOf(map[string][]changes.Change{"TestScenarioJ": {
		{ID: 1, EntryPoint: "Join", Reason: myapp.ReasonMemberJoined, Cause: "request:r", CorrelationID: "r", Actor: "human"},
		{ID: 2, EntryPoint: "Join", Reason: myapp.ReasonWelcomeNoteWritten, Cause: "change:1", CorrelationID: "r", Actor: "human", Rule: "EveryNewMemberIsWelcomed"},
		{ID: 3, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:s", CorrelationID: "s", Actor: "member:1", Rule: "SomeRule"},
	}})
	welcomed := []flowRule{{Reason: "WelcomeNoteWritten", Rule: "EveryNewMemberIsWelcomed"}}
	if len(m.Policies) != 1 || !reflect.DeepEqual(m.Policies[0].Rules, welcomed) {
		t.Errorf("the MemberJoined to WelcomeNoteWritten policy's rules: got %+v, want %+v", m.Policies, welcomed)
	}
	got := map[string][]flowRule{}
	for _, e := range m.EntryPoints {
		got[e.Name] = e.Rules
	}
	want := map[string][]flowRule{"Join": {}, "WriteNote": {{Reason: "NoteWritten", Rule: "SomeRule"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entry points' rules: got %#v, want %#v", got, want)
	}
	for _, c := range m.Chains {
		if c.EntryPoint == "Join" && (len(c.Then) != 1 || !reflect.DeepEqual(c.Then[0].Rules, welcomed)) {
			t.Errorf("the chain's hop rules: got %+v, want %+v", c.Then, welcomed)
		}
	}
	if !strings.Contains(string(m.json()), `"rules": []`) {
		t.Errorf("an entry point with no rule is not \"rules\": [] in flow.json:\n%s", m.json())
	}
	if doc := string(m.markdown()); !strings.Contains(doc, "- `WelcomeNoteWritten`: EveryNewMemberIsWelcomed\n") ||
		!strings.Contains(doc, "- `NoteWritten`: SomeRule\n") {
		t.Errorf("flow.md does not list the rules per reason:\n%s", doc)
	}
}

// The declarations come off the app, not the records: its name, every
// invariant (reasons_<context>.go) and what each reason writes,
// tables only (ReasonWrites).
func TestFlowCarriesTheAppsDeclarations(t *testing.T) {
	t.Parallel()
	m := flowOf(map[string][]changes.Change{})
	if m.App != "myapp" {
		t.Errorf("app = %q, want %q", m.App, "myapp")
	}
	// expected: internal/myapp/reasons_notes.go, notesInvariants
	invs := []flowInvariant{
		{Name: "ACorrectionChangesSomething", Context: "notes", Rule: "A correction must change the note's title or its body.",
			Guards: []string{"NoteCorrected"}, Shapes: map[string][]string{}},
		{Name: "EveryNewMemberIsWelcomed", Context: "notes", Rule: "Every member who joins gets a welcome note.",
			Guards: []string{}, Shapes: map[string][]string{"WelcomeNoteWritten": {"notes"}}},
	}
	if !reflect.DeepEqual(m.Invariants, invs) {
		t.Errorf("invariants: got %#v, want %#v", m.Invariants, invs)
	}
	// expected: internal/myapp/reasons_accounts.go and reasons_notes.go, <context>Writes
	ws := []flowWrites{
		{Reason: "MemberJoined", Context: "accounts", Tables: []string{"members"}},
		{Reason: "NoteCorrected", Context: "notes", Tables: []string{"notes"}},
		{Reason: "NoteWritten", Context: "notes", Tables: []string{"notes"}},
		{Reason: "WelcomeNoteWritten", Context: "notes", Tables: []string{"notes"}},
	}
	if !reflect.DeepEqual(m.Writes, ws) {
		t.Errorf("writes: got %#v, want %#v", m.Writes, ws)
	}
}

// Every name is located in the app's source: an entry point where a
// call (or the Trail's EntryPoint) first takes it as a string literal,
// a reason and an invariant where reasons_<context>.go declares it, a
// table at its struct in models.go. Files are relative to the app's
// root, and each line is the one that says it.
func TestFlowLocatesEveryNameInTheSource(t *testing.T) {
	t.Parallel()
	m := flowOf(map[string][]changes.Change{"TestScenarioL": {
		{ID: 1, EntryPoint: "Join", Reason: myapp.ReasonMemberJoined, Cause: "request:r", CorrelationID: "r"},
		{ID: 2, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:s", CorrelationID: "s"},
		{ID: 3, EntryPoint: "CorrectNote", Reason: myapp.ReasonNoteCorrected, Cause: "request:u", CorrelationID: "u"},
	}})
	if err := m.locate(appRoot); err != nil {
		t.Fatalf("locate: %v", err)
	}
	says := func(kind, name string, got map[string]flowSource, file, text string) {
		t.Helper()
		src, ok := got[name]
		if !ok || src.File != file {
			t.Errorf("%s %s: source %+v, want in %s", kind, name, src, file)
			return
		}
		b, err := os.ReadFile(filepath.Join(appRoot, src.File))
		if err != nil {
			t.Fatalf("%s %s: %v", kind, name, err)
		}
		lines := strings.Split(string(b), "\n")
		if src.Line < 1 || src.Line > len(lines) || !strings.Contains(lines[src.Line-1], text) {
			t.Errorf("%s %s: %s:%d does not say %q", kind, name, src.File, src.Line, text)
		}
	}
	says("entry point", "Join", m.Sources.EntryPoints, "internal/myapp/handlers.go", `t.EntryPoint = "Join"`)
	says("entry point", "WriteNote", m.Sources.EntryPoints, "internal/myapp/handlers.go", `a.trail(r, "WriteNote")`)
	says("entry point", "CorrectNote", m.Sources.EntryPoints, "internal/myapp/handlers.go", `a.trail(r, "CorrectNote")`)
	says("reason", "MemberJoined", m.Sources.Reasons, "internal/myapp/reasons_accounts.go", `ReasonMemberJoined = "MemberJoined"`)
	says("reason", "WelcomeNoteWritten", m.Sources.Reasons, "internal/myapp/reasons_notes.go", `ReasonWelcomeNoteWritten = "WelcomeNoteWritten"`)
	says("table", "members", m.Sources.Tables, "internal/myapp/models.go", "type Member struct")
	says("table", "notes", m.Sources.Tables, "internal/myapp/models.go", "type Note struct")
	says("invariant", "EveryNewMemberIsWelcomed", m.Sources.Invariants, "internal/myapp/reasons_notes.go", `RuleEveryNewMemberIsWelcomed = "EveryNewMemberIsWelcomed"`)
	for _, inv := range m.Invariants {
		if want := m.Sources.Invariants[inv.Name]; inv.Source != want || want.Line == 0 {
			t.Errorf("invariant %s: source %+v, sources.invariants has %+v", inv.Name, inv.Source, want)
		}
	}
	if len(m.Sources.Reasons) != len(myapp.Reasons) || len(m.Sources.Invariants) != len(myapp.Invariants) {
		t.Errorf("sources: %d reasons and %d invariants, want %d and %d", len(m.Sources.Reasons), len(m.Sources.Invariants), len(myapp.Reasons), len(myapp.Invariants))
	}
}

// The census: every entry point, reason, table and invariant in the
// map has a source, and one that has none is named. flowBuild fails on
// it, so make flow and make flow-check do.
func TestFlowCensusNamesWhatHasNoSource(t *testing.T) {
	t.Parallel()
	by := map[string][]changes.Change{"TestScenarioN": {
		{ID: 1, EntryPoint: "WriteNote", Reason: myapp.ReasonNoteWritten, Cause: "request:r", CorrelationID: "r"},
		{ID: 2, EntryPoint: "NoSuchEntryPoint", Reason: myapp.ReasonNoteWritten, Cause: "request:s", CorrelationID: "s"},
	}}
	_, err := flowBuild(by, appRoot)
	if want := "entry point NoSuchEntryPoint has no source"; err == nil || err.Error() != want {
		t.Errorf("flowBuild over the app: got %v, want %q", err, want)
	}
	// Positive: every name the app's own records carry is found.
	if _, err := flowBuild(map[string][]changes.Change{"TestScenarioN": by["TestScenarioN"][:1]}, appRoot); err != nil {
		t.Errorf("flowBuild over the app's own names: %v", err)
	}
	// An empty root: every kind's gap is named.
	_, err = flowBuild(by, t.TempDir())
	for _, want := range []string{
		"entry point WriteNote has no source",
		"reason NoteWritten has no source",
		"table notes has no source",
		"invariant EveryNewMemberIsWelcomed has no source",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("flowBuild over an empty root: got %v, want it to name %q", err, want)
		}
	}
}
