// Command blueprint draws a Blueprint graph of this repository's apps
// from what they already commit: the root .blueprint.config.json
// (its framework) and every apps/*/docs/flow.json (the map make flow
// reads off the scenarios' change records). It imports no app.
//
//	blueprint [-root dir] [-out dir]
//
// writes <out>/graph.json (default <root>/.blueprint):
// {schemaVersion: 1, framework, subdomains, nodes, edges}, where a
// context is a subdomain, an entry point a command in each context it
// lands in, a reason an event, a cross-context policy a policy named
// {From}Then{To} in the target context, an invariant an invariant
// (rule, errorCodes [name]), a table a reason writes a datastore and an
// agent:<name> actor an external system (builder.app has the edges).
// Labels are capital case; meta keeps the app, the code's spelling
// (runtimeName) and the declaration (source). Ids are blueprint-go's
// (NodeKey). Nodes are sorted by id and edges by
// from, kind, to, pretty-printed with a trailing newline and no
// timestamp, so the same flow maps write the same bytes.
//
// Beside it, <out>/diagnostics.json (diagnostics.go): each edge the
// non-ddd grammar refuses (left out of the graph), each context two
// apps declare and each id two names give, each node with no edge, and
// the flow maps' findings. Both files are written atomically. The run
// exits 1 on a refused edge or a collision, never on the rest.
//
// A flow.json older than version 2 lacks what the graph is built
// from: it is refused, naming the app to run make flow in, as a flow
// map in another app's directory is. A refused input writes no file.
package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// flowVersion is the oldest flow.json the graph can be built from.
const flowVersion = 2

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, _, stderr io.Writer) int {
	fs := flag.NewFlagSet("blueprint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "the repository root: .blueprint.config.json and apps/*/docs/flow.json")
	out := fs.String("out", "", "where graph.json and diagnostics.json go (default <root>/.blueprint)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		*out = filepath.Join(*root, ".blueprint")
	}
	g, d, err := extract(*root)
	if err != nil {
		fmt.Fprintln(stderr, "blueprint:", err)
		return 1
	}
	return report(*out, g, d, stderr)
}

type config struct {
	Framework string `json:"framework"`
}

// flow is the part of a flow.json the graph reads.
type flow struct {
	Version     int         `json:"version"`
	App         string      `json:"app"`
	EntryPoints []entry     `json:"entry_points"`
	Policies    []policy    `json:"policies"`
	Invariants  []invariant `json:"invariants"`
	Writes      []writes    `json:"writes"`
	Sources     sources     `json:"sources"`
	// The flow map's findings, carried into diagnostics.json.
	Unnamed           []entry        `json:"unnamed"`
	Empty             []emptyFinding `json:"empty"`
	ImplicitCrossings []crossFinding `json:"implicit_crossings"`
}

// emptyFinding is a change no entry point opened.
type emptyFinding struct {
	Scenario string `json:"scenario"`
	ChangeID int64  `json:"change_id"`
	Event    fact   `json:"event"`
}

// crossFinding is a change outside its entry point's home context that
// no policy reached.
type crossFinding struct {
	Scenario   string `json:"scenario"`
	ChangeID   int64  `json:"change_id"`
	Event      fact   `json:"event"`
	EntryPoint string `json:"entry_point"`
	Home       string `json:"home"`
}

type entry struct {
	Name    string   `json:"name"`
	Context string   `json:"context"`
	Reasons []string `json:"reasons"`
	Actors  []string `json:"actors"`
	Rules   []ruled  `json:"rules"`
}

// ruled is a reason landed under a named rule (Trail.By).
type ruled struct {
	Reason string `json:"reason"`
	Rule   string `json:"rule"`
}

// policy is one context's fact causing another's, in one request.
type policy struct {
	From  fact    `json:"from"`
	To    fact    `json:"to"`
	Rules []ruled `json:"rules"`
}

type fact struct {
	Reason  string `json:"reason"`
	Context string `json:"context"`
}

type invariant struct {
	Name    string              `json:"name"`
	Context string              `json:"context"`
	Rule    string              `json:"rule"`
	Guards  []string            `json:"guards"`
	Shapes  map[string][]string `json:"shapes"`
}

// writes is the tables a reason may write.
type writes struct {
	Reason  string   `json:"reason"`
	Context string   `json:"context"`
	Tables  []string `json:"tables"`
}

// sources maps each name to its declaration, the file relative to the
// app's root.
type sources struct {
	EntryPoints map[string]source `json:"entry_points"`
	Reasons     map[string]source `json:"reasons"`
	Tables      map[string]source `json:"tables"`
	Invariants  map[string]source `json:"invariants"`
}

type graph struct {
	SchemaVersion int      `json:"schemaVersion"`
	Framework     string   `json:"framework"`
	Subdomains    []string `json:"subdomains"`
	Nodes         []node   `json:"nodes"`
	Edges         []edge   `json:"edges"`
}

type node struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	Subdomain string `json:"subdomain"`
	// Rule and ErrorCodes are an invariant's: its sentence, and its
	// name as the code a refusal carries.
	Rule       string   `json:"rule,omitempty"`
	ErrorCodes []string `json:"errorCodes,omitempty"`
	Meta       meta     `json:"meta"`
}

// meta says where a node comes from: its app, and its name as the
// code spells it (the label is capital case).
type meta struct {
	App         string  `json:"app"`
	RuntimeName string  `json:"runtimeName"`
	Source      *source `json:"source,omitempty"`
}

// source is where a name is declared: a repository-relative file and
// its line.
type source struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// extract builds the graph of every app under root, and the
// diagnostics read off the flow maps: each app's flow findings and the
// name collisions across them. report adds the rest.
func extract(root string) (graph, diagnostics, error) {
	d := newDiagnostics()
	cfg, err := readConfig(filepath.Join(root, ".blueprint.config.json"))
	if err != nil {
		return graph{}, d, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "apps", "*", "docs", "flow.json"))
	if err != nil {
		return graph{}, d, err
	}
	if len(paths) == 0 {
		return graph{}, d, fmt.Errorf("no apps/*/docs/flow.json under %s", root)
	}
	b := builder{byID: map[string]*node{}, seenEdge: map[edge]bool{}, names: map[string][]claim{}, apps: map[string][]string{}}
	for _, p := range paths {
		app := filepath.Base(filepath.Dir(filepath.Dir(p)))
		f, err := readFlow(p, app)
		if err != nil {
			return graph{}, d, err
		}
		if err := b.app(app, f); err != nil {
			return graph{}, d, err
		}
		d.findings(app, f)
	}
	d.NameCollisions = b.collisions()
	return b.graph(cfg.Framework), d, nil
}

// app adds one app's flow map to the graph: a command per (entry point,
// context) producing its reasons' events, guarded by each invariant
// that guards or shapes one of them or that its rules name, and invoked
// by each agent:<name> actor's external system; a policy per
// cross-context hop, {From}Then{To} in the target context, triggered
// by its source event, producing its target event and guarded by the
// rules the hop names; each event writing to its tables' datastores.
// meta.source is the name's declaration, repository-relative; a
// policy's is its target reason's, an external system's its first
// entry point's.
func (b *builder) app(app string, f flow) error {
	at := func(m map[string]source, name string) *source {
		s, ok := m[name]
		if !ok {
			return nil
		}
		return &source{File: path.Join("apps", app, s.File), Line: s.Line}
	}
	event := func(context, reason string) string {
		return b.node(app, context, "event", reason, at(f.Sources.Reasons, reason))
	}
	invariants := map[string]string{}
	for _, i := range f.Invariants {
		id := b.node(app, i.Context, "invariant", i.Name, at(f.Sources.Invariants, i.Name))
		b.byID[id].Rule, b.byID[id].ErrorCodes = i.Rule, []string{i.Name}
		invariants[i.Name] = id
	}
	guard := func(from string, rules []ruled) error {
		for _, r := range rules {
			inv, ok := invariants[r.Rule]
			if !ok {
				return fmt.Errorf("apps/%s: rule %s on %s names no declared invariant: run make flow in apps/%s", app, r.Rule, r.Reason, app)
			}
			b.edge(from, "guarded-by", inv)
		}
		return nil
	}
	for _, e := range f.EntryPoints {
		cmd := b.node(app, e.Context, "command", e.Name, at(f.Sources.EntryPoints, e.Name))
		for _, r := range e.Reasons {
			b.edge(cmd, "produces", event(e.Context, r))
			for _, i := range f.Invariants {
				if _, shapes := i.Shapes[r]; shapes || slices.Contains(i.Guards, r) {
					b.edge(cmd, "guarded-by", invariants[i.Name])
				}
			}
		}
		if err := guard(cmd, e.Rules); err != nil {
			return err
		}
		for _, a := range e.Actors {
			if name, ok := strings.CutPrefix(a, "agent:"); ok {
				b.edge(b.node(app, e.Context, "external-system", name, at(f.Sources.EntryPoints, e.Name)), "invokes", cmd)
			}
		}
	}
	for _, p := range f.Policies {
		pol := b.node(app, p.To.Context, "policy", p.From.Reason+"Then"+p.To.Reason, at(f.Sources.Reasons, p.To.Reason))
		b.edge(event(p.From.Context, p.From.Reason), "triggers", pol)
		b.edge(pol, "produces", event(p.To.Context, p.To.Reason))
		if err := guard(pol, p.Rules); err != nil {
			return err
		}
	}
	for _, w := range f.Writes {
		ev := event(w.Context, w.Reason)
		for _, t := range w.Tables {
			b.edge(ev, "writes-to", b.node(app, w.Context, "datastore", t, at(f.Sources.Tables, t)))
		}
	}
	return nil
}

func readConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Framework == "" {
		return cfg, fmt.Errorf("%s names no framework", path)
	}
	// The diagnostics hold the graph to the non-ddd grammar alone: under
	// any other framework graph.json would claim a grammar nothing here
	// checked it against.
	if cfg.Framework != "non-ddd" {
		return cfg, fmt.Errorf("%s names framework %q: the graph is drawn under non-ddd only", path, cfg.Framework)
	}
	return cfg, nil
}

func readFlow(path, app string) (flow, error) {
	var f flow
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version < flowVersion {
		return f, fmt.Errorf("%s is version %d, the graph needs %d: run make flow in apps/%s", path, f.Version, flowVersion, app)
	}
	if f.App != "" && f.App != app {
		return f, fmt.Errorf("apps/%s/docs/flow.json is app %s's flow map: run make flow in apps/%s", app, f.App, app)
	}
	return f, nil
}

type builder struct {
	byID     map[string]*node
	edges    []edge
	seenEdge map[edge]bool
	// names is each id's distinct names, first found first: an id two
	// names give is a collision.
	names map[string][]claim
	// apps is each context's apps, in the order they were read: a
	// context two apps declare is a collision.
	apps map[string][]string
}

// claim is a name an app gave an id, and where it is declared.
type claim struct {
	app, name string
	src       *source
}

// node adds the node of that type and name in a context, once however
// many entry points land it, and returns its id. It notes the context's
// app and, when it differs from the id's earlier ones, the name.
func (b *builder) node(app, context, typ, name string, src *source) string {
	id := NodeKey(context, typ, name)
	if !slices.Contains(b.apps[context], app) {
		b.apps[context] = append(b.apps[context], app)
	}
	if !slices.ContainsFunc(b.names[id], func(c claim) bool { return c.name == name }) {
		b.names[id] = append(b.names[id], claim{app: app, name: name, src: src})
	}
	if b.byID[id] == nil {
		b.byID[id] = &node{ID: id, Type: typ, Label: capitalCase(name), Subdomain: context,
			Meta: meta{App: app, RuntimeName: name, Source: src}}
	}
	return id
}

// edge adds an edge once, however many clauses find it.
func (b *builder) edge(from, kind, to string) {
	e := edge{From: from, To: to, Kind: kind}
	if !b.seenEdge[e] {
		b.seenEdge[e] = true
		b.edges = append(b.edges, e)
	}
}

func (b *builder) graph(framework string) graph {
	nodes := []node{}
	for _, n := range b.byID {
		nodes = append(nodes, *n)
	}
	slices.SortFunc(nodes, func(x, y node) int { return strings.Compare(x.ID, y.ID) })
	sortEdges(b.edges)
	subdomains := []string{}
	for _, n := range nodes {
		if !slices.Contains(subdomains, n.Subdomain) {
			subdomains = append(subdomains, n.Subdomain)
		}
	}
	slices.Sort(subdomains)
	return graph{SchemaVersion: 1, Framework: framework, Subdomains: subdomains,
		Nodes: nodes, Edges: append([]edge{}, b.edges...)}
}

// sortEdges orders edges by from, then kind, then to.
func sortEdges(es []edge) {
	slices.SortFunc(es, func(x, y edge) int {
		return cmp.Or(strings.Compare(x.From, y.From), strings.Compare(x.Kind, y.Kind), strings.Compare(x.To, y.To))
	})
}

// capitalCase is a node's label, capital case as design.md has pv3
// write them: the words Slugify finds, each capitalised ("WriteNote" is
// "Write Note"), so a label slugs to its runtime name's id.
func capitalCase(name string) string {
	words := strings.Split(Slugify(name), "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// write writes v to path as pretty JSON with a trailing newline,
// atomically: a temporary sibling, renamed over path once whole.
func write(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
