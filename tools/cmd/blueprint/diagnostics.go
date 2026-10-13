package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// rule is one edge the grammar allows: a node type, an edge kind, a
// node type.
type rule struct{ From, Kind, To string }

// nonDDDRules is the non-ddd grammar, design.md's nonDDDRules list in
// its order (TestTheGrammarIsTheDesignsList): the seven edges the
// extractor draws, and ui invokes command, which it never draws.
// design.md gives blueprint-go's design/vocabulary.go the other copy.
var nonDDDRules = []rule{
	{"command", "produces", "event"},
	{"command", "guarded-by", "invariant"},
	{"event", "triggers", "policy"},
	{"policy", "produces", "event"},
	{"policy", "guarded-by", "invariant"},
	{"external-system", "invokes", "command"},
	{"event", "writes-to", "datastore"},
	{"ui", "invokes", "command"},
}

// diagnostics is diagnostics.json: pv3's eight arrays in
// blueprint-schema's order, the five this extractor has nothing for
// left empty, then the flow map's findings as extra keys. Every array
// is present, empty or not (newDiagnostics).
type diagnostics struct {
	UnresolvedEdges             []struct{}  `json:"unresolvedEdges"`
	DynamicNameSites            []struct{}  `json:"dynamicNameSites"`
	NameCollisions              []collision `json:"nameCollisions"`
	LeafResourceLabelCollisions []struct{}  `json:"leafResourceLabelCollisions"`
	OrphanNodes                 []orphan    `json:"orphanNodes"`
	TopologyViolations          []violation `json:"topologyViolations"`
	StaleOverrides              []struct{}  `json:"staleOverrides"`
	OpaqueAccessSites           []struct{}  `json:"opaqueAccessSites"`
	UnnamedEntryPoints          []unnamed   `json:"unnamedEntryPoints"`
	EmptyEntryPoints            []empty     `json:"emptyEntryPoints"`
	MixedContextRequests        []mixed     `json:"mixedContextRequests"`
	ImplicitCrossings           []crossing  `json:"implicitCrossings"`
}

func newDiagnostics() diagnostics {
	return diagnostics{
		UnresolvedEdges: []struct{}{}, DynamicNameSites: []struct{}{}, NameCollisions: []collision{},
		LeafResourceLabelCollisions: []struct{}{}, OrphanNodes: []orphan{}, TopologyViolations: []violation{},
		StaleOverrides: []struct{}{}, OpaqueAccessSites: []struct{}{}, UnnamedEntryPoints: []unnamed{},
		EmptyEntryPoints: []empty{}, MixedContextRequests: []mixed{}, ImplicitCrossings: []crossing{},
	}
}

// collision is one name two declarations claim: a context two apps
// declare (type "subdomain"), or a node id two names give.
type collision struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Sites []site `json:"sites"`
}

type site struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// flowSite is an app's flow map, the site of what it declares no
// source for.
func flowSite(app string) site {
	return site{File: "apps/" + app + "/docs/flow.json", Line: 1}
}

type orphan struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// violation is an edge the grammar does not allow; a type is empty
// when its endpoint is no node.
type violation struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	FromType string `json:"fromType"`
	ToType   string `json:"toType"`
}

// unnamed, empty and crossing are flow.json's unnamed, empty and
// implicit_crossings entries, each with its app.
type unnamed struct {
	App     string   `json:"app"`
	Name    string   `json:"name"`
	Context string   `json:"context"`
	Reasons []string `json:"reasons"`
	Actors  []string `json:"actors"`
}

type empty struct {
	App      string `json:"app"`
	Scenario string `json:"scenario"`
	ChangeID int64  `json:"changeId"`
	Event    fact   `json:"event"`
}

type crossing struct {
	App        string `json:"app"`
	Scenario   string `json:"scenario"`
	ChangeID   int64  `json:"changeId"`
	Event      fact   `json:"event"`
	EntryPoint string `json:"entryPoint"`
	Home       string `json:"home"`
}

// mixed is an entry point that lands in more than one context: a
// command in each (fork F3).
type mixed struct {
	App        string   `json:"app"`
	EntryPoint string   `json:"entryPoint"`
	Contexts   []string `json:"contexts"`
	Commands   []string `json:"commands"`
}

// findings adds one app's flow findings to d: its unnamed entry points,
// its changes with no entry point and its implicit crossings as the
// flow map lists them, and each entry point its entry_points list puts
// in more than one context (that list holds a name once per context),
// by name.
func (d *diagnostics) findings(app string, f flow) {
	for _, u := range f.Unnamed {
		d.UnnamedEntryPoints = append(d.UnnamedEntryPoints, unnamed{App: app, Name: u.Name, Context: u.Context, Reasons: u.Reasons, Actors: u.Actors})
	}
	for _, e := range f.Empty {
		d.EmptyEntryPoints = append(d.EmptyEntryPoints, empty{App: app, Scenario: e.Scenario, ChangeID: e.ChangeID, Event: e.Event})
	}
	contexts := map[string][]string{}
	for _, e := range f.EntryPoints {
		contexts[e.Name] = append(contexts[e.Name], e.Context)
	}
	for _, name := range slices.Sorted(maps.Keys(contexts)) {
		ctxs := slices.Sorted(slices.Values(contexts[name]))
		if len(ctxs) < 2 {
			continue
		}
		m := mixed{App: app, EntryPoint: name, Contexts: ctxs}
		for _, c := range ctxs {
			m.Commands = append(m.Commands, NodeKey(c, "command", name))
		}
		d.MixedContextRequests = append(d.MixedContextRequests, m)
	}
	for _, c := range f.ImplicitCrossings {
		d.ImplicitCrossings = append(d.ImplicitCrossings, crossing{App: app, Scenario: c.Scenario, ChangeID: c.ChangeID, Event: c.Event, EntryPoint: c.EntryPoint, Home: c.Home})
	}
}

// collisions is every context more than one app declares, a site per
// app's flow map, and every id more than one name gives, a site per
// name; by name, then type.
func (b *builder) collisions() []collision {
	out := []collision{}
	for _, ctx := range slices.Sorted(maps.Keys(b.apps)) {
		if apps := b.apps[ctx]; len(apps) > 1 {
			c := collision{Name: ctx, Type: "subdomain"}
			for _, a := range apps {
				c.Sites = append(c.Sites, flowSite(a))
			}
			out = append(out, c)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(b.names)) {
		if claims := b.names[id]; len(claims) > 1 {
			c := collision{Name: id, Type: b.byID[id].Type}
			for _, cl := range claims {
				s := flowSite(cl.app)
				if cl.src != nil {
					s = site(*cl.src)
				}
				c.Sites = append(c.Sites, s)
			}
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(x, y collision) int {
		return cmp.Or(strings.Compare(x.Name, y.Name), strings.Compare(x.Type, y.Type))
	})
	return out
}

// report is how every run whose input was read ends: it checks g's edges against the
// grammar, leaving out of graph.json each one the grammar refuses and
// naming it in topologyViolations, reports the nodes left with no edge
// as orphans, writes graph.json and diagnostics.json into out, and
// fails (exit 1) only on a topology violation or a name collision
// (fork F5). Everything else is reported, never failed (ADR 0003).
func report(out string, g graph, d diagnostics, stderr io.Writer) int {
	g, d.TopologyViolations = checkTopology(g)
	d.OrphanNodes = orphans(g)
	err := write(filepath.Join(out, "graph.json"), g)
	if err == nil {
		err = write(filepath.Join(out, "diagnostics.json"), d)
	}
	if err != nil {
		fmt.Fprintln(stderr, "blueprint:", err)
		return 1
	}
	if len(d.TopologyViolations)+len(d.NameCollisions) > 0 {
		fmt.Fprintf(stderr, "blueprint: %d topology violation(s) and %d name collision(s): see %s\n",
			len(d.TopologyViolations), len(d.NameCollisions), filepath.Join(out, "diagnostics.json"))
		return 1
	}
	return 0
}

// checkTopology splits g's edges into those nonDDDRules allows, kept,
// and the rest, returned as violations in edge order. An edge whose
// endpoint is no node is a violation.
func checkTopology(g graph) (graph, []violation) {
	types := map[string]string{}
	for _, n := range g.Nodes {
		types[n.ID] = n.Type
	}
	legal, bad := []edge{}, []violation{}
	for _, e := range g.Edges {
		from, to := types[e.From], types[e.To]
		if from != "" && to != "" && slices.Contains(nonDDDRules, rule{from, e.Kind, to}) {
			legal = append(legal, e)
			continue
		}
		bad = append(bad, violation{From: e.From, To: e.To, Kind: e.Kind, FromType: from, ToType: to})
	}
	g.Edges = legal
	return g, bad
}

// orphans is every node no edge of g touches, in node order.
func orphans(g graph) []orphan {
	touched := map[string]bool{}
	for _, e := range g.Edges {
		touched[e.From], touched[e.To] = true, true
	}
	out := []orphan{}
	for _, n := range g.Nodes {
		if !touched[n.ID] {
			out = append(out, orphan{ID: n.ID, Type: n.Type})
		}
	}
	return out
}
