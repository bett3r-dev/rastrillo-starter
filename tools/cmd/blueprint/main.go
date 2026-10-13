// Command blueprint draws a Blueprint graph of this repository's apps
// from what they already commit: the root .blueprint.config.json
// (its framework) and every apps/*/docs/flow.json (the map make flow
// reads off the scenarios' change records). It imports no app.
//
//	blueprint [-root dir] [-out dir]
//
// writes <out>/graph.json (default <root>/.blueprint):
// {schemaVersion: 1, framework, subdomains, nodes, edges}, where an
// entry point is a command, a reason an event and a context a
// subdomain, and a command produces each event its entry point lands.
// Ids are blueprint-go's (NodeKey). Nodes are sorted by id and edges by
// from, kind, to, pretty-printed with a trailing newline and no
// timestamp, so the same flow maps write the same bytes.
//
// A flow.json older than version 2 lacks what the graph is built
// from: it is refused, naming the app to run make flow in.
package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
	out := fs.String("out", "", "where graph.json goes (default <root>/.blueprint)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		*out = filepath.Join(*root, ".blueprint")
	}
	g, err := extract(*root)
	if err == nil {
		err = write(filepath.Join(*out, "graph.json"), g)
	}
	if err != nil {
		fmt.Fprintln(stderr, "blueprint:", err)
		return 1
	}
	return 0
}

type config struct {
	Framework string `json:"framework"`
}

// flow is the part of a flow.json the graph reads.
type flow struct {
	Version     int     `json:"version"`
	EntryPoints []entry `json:"entry_points"`
}

type entry struct {
	Name    string   `json:"name"`
	Context string   `json:"context"`
	Reasons []string `json:"reasons"`
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
	Meta      meta   `json:"meta"`
}

// meta says where a node comes from: its app, and its name as the
// code spells it (the label is capital case).
type meta struct {
	App         string `json:"app"`
	RuntimeName string `json:"runtimeName"`
}

type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// extract builds the graph of every app under root.
func extract(root string) (graph, error) {
	cfg, err := readConfig(filepath.Join(root, ".blueprint.config.json"))
	if err != nil {
		return graph{}, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "apps", "*", "docs", "flow.json"))
	if err != nil {
		return graph{}, err
	}
	if len(paths) == 0 {
		return graph{}, fmt.Errorf("no apps/*/docs/flow.json under %s", root)
	}
	b := builder{seen: map[string]bool{}}
	for _, p := range paths {
		app := filepath.Base(filepath.Dir(filepath.Dir(p)))
		f, err := readFlow(p, app)
		if err != nil {
			return graph{}, err
		}
		for _, e := range f.EntryPoints {
			cmd := b.node(app, e.Context, "command", e.Name)
			for _, r := range e.Reasons {
				b.edges = append(b.edges, edge{From: cmd, To: b.node(app, e.Context, "event", r), Kind: "produces"})
			}
		}
	}
	return b.graph(cfg.Framework), nil
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
	return f, nil
}

type builder struct {
	nodes []node
	edges []edge
	seen  map[string]bool
}

// node adds the node of that type and name in a context, once however
// many entry points land it, and returns its id.
func (b *builder) node(app, context, typ, name string) string {
	id := NodeKey(context, typ, name)
	if !b.seen[id] {
		b.seen[id] = true
		b.nodes = append(b.nodes, node{ID: id, Type: typ, Label: capitalCase(name), Subdomain: context,
			Meta: meta{App: app, RuntimeName: name}})
	}
	return id
}

func (b *builder) graph(framework string) graph {
	slices.SortFunc(b.nodes, func(x, y node) int { return strings.Compare(x.ID, y.ID) })
	sortEdges(b.edges)
	subdomains := []string{}
	for _, n := range b.nodes {
		if !slices.Contains(subdomains, n.Subdomain) {
			subdomains = append(subdomains, n.Subdomain)
		}
	}
	slices.Sort(subdomains)
	return graph{SchemaVersion: 1, Framework: framework, Subdomains: subdomains,
		Nodes: append([]node{}, b.nodes...), Edges: append([]edge{}, b.edges...)}
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

func write(path string, g graph) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
