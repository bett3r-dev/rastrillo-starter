package myapptest

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"amadan.net/rastrillo/rastrillo/db"

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
// The one failure is drift. The extraction needs every scenario, so
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
	m := flowOf(landed.by)
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
const flowVersion = 1

type flowMap struct {
	Version           int            `json:"version"`
	EntryPoints       []flowEntry    `json:"entry_points"`
	Unnamed           []flowEntry    `json:"unnamed"`
	Policies          []flowPolicy   `json:"policies"`
	Chains            []flowChain    `json:"chains"`
	Empty             []flowEmpty    `json:"empty"`
	Mixed             []flowMixed    `json:"mixed"`
	ImplicitCrossings []flowCrossing `json:"implicit_crossings"`
}

// flowEntry is what one entry point lands directly in one context.
// Actors are who opened it: "member", "human" or "agent:<name>".
type flowEntry struct {
	Name    string   `json:"name"`
	Context string   `json:"context"`
	Reasons []string `json:"reasons"`
	Actors  []string `json:"actors"`
}

type flowEvent struct {
	Reason  string `json:"reason"`
	Context string `json:"context"`
}

func (e flowEvent) String() string { return e.Reason + "[" + e.Context + "]" }

type flowPolicy struct {
	From      flowEvent `json:"from"`
	To        flowEvent `json:"to"`
	Scenarios []string  `json:"scenarios"`
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
	if strings.HasPrefix(a, "member:") {
		return "member"
	}
	return a
}

// flowOf builds the map from the scenarios' changes.
func flowOf(by map[string][]changes.Change) flowMap {
	type set = map[string]bool
	direct := map[string]map[string]set{} // entry point -> context -> reason
	actors := map[string]set{}            // entry point -> actor kind
	policies := map[[2]flowEvent]set{}    // -> scenarios
	chains := map[string]*flowChain{}     // by line
	m := flowMap{Version: flowVersion}

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
			if strings.HasPrefix(c.Cause, "change:") {
				p, ok := parentOf(c, byID)
				if pev := eventOf(p); ok && pev.Context != ev.Context {
					a := [2]flowEvent{pev, ev}
					if policies[a] == nil {
						policies[a] = set{}
					}
					policies[a][scen] = true
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
				mx.Contexts = append(mx.Contexts, flowEntry{Name: ep, Context: ctx, Reasons: uniq(ctxs[ctx]), Actors: []string{}})
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
			e := flowEntry{Name: ep, Context: ctx, Reasons: sortedKeys(direct[ep][ctx]), Actors: sortedKeys(actors[ep])}
			if unnamed(ep) {
				m.Unnamed = append(m.Unnamed, e)
			} else {
				m.EntryPoints = append(m.EntryPoints, e)
			}
		}
	}
	m.Policies = []flowPolicy{}
	for a, scens := range policies {
		m.Policies = append(m.Policies, flowPolicy{From: a[0], To: a[1], Scenarios: sortedKeys(scens)})
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
	sort.SliceStable(m.Mixed, func(i, j int) bool {
		return m.Mixed[i].EntryPoint+m.Mixed[i].Scenario < m.Mixed[j].EntryPoint+m.Mixed[j].Scenario
	})
	return m
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
				next = append(append([]flowPolicy{}, path...), flowPolicy{From: from, To: to, Scenarios: []string{}})
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
	b.WriteString("## Entry points\n\nWhat each named entry point lands directly: caused by the request, not by another change.\n\n")
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

// parentOf is the change c's cause names, when it names one.
func parentOf(c changes.Change, byID map[int64]changes.Change) (changes.Change, bool) {
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
	doc := string(flowOf(map[string][]changes.Change{
		"TestScenarioX": {
			direct(1, "", myapp.ReasonNoteWritten),
			direct(2, "POST /notes/{id}/archive", myapp.ReasonNoteCorrected),
			direct(3, "Join", myapp.ReasonMemberJoined),
			direct(4, "Join", myapp.ReasonWelcomeNoteWritten),
		},
	}).markdown())
	for _, want := range []string{
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
