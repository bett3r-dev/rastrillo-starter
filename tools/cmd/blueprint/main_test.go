package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runOver runs the program over root into a fresh directory, as
// make blueprint does: its exit code, its stderr and the directory.
func runOver(t *testing.T, root string) (int, string, string) {
	t.Helper()
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root, "-out", out}, &stdout, &stderr)
	return code, stderr.String(), out
}

// A v2 flow.json covering every row of design.md's mapping becomes the
// graph written by hand in want-graph.json (entry point → command per
// context, reason → event, context → subdomain, cross-context policy →
// policy {From}Then{To} in the target context, invariant → invariant
// with rule and errorCodes, written table → datastore, agent:<name>
// actor → external-system, meta.source repository-relative; ids by
// NodeKey; nodes by id, edges by from, kind, to; framework from the
// root .blueprint.config.json): byte for byte, so the pretty print,
// the trailing newline and the absence of a timestamp are held too.
// NoteWritten, landed by two entry points, is one node.
func TestAV2FlowBecomesItsGraph(t *testing.T) {
	t.Parallel()
	root := filepath.Join("testdata", "mapping")
	code, stderr, out := runOver(t, root)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	got, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "want-graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("graph.json\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// A flow.json from before version 2 lacks what the graph is built
// from: the program refuses it, naming the app to regenerate, and
// writes no graph.
func TestAnOldFlowIsRefused(t *testing.T) {
	t.Parallel()
	code, stderr, out := runOver(t, filepath.Join("testdata", "old-flow"))
	if code == 0 {
		t.Errorf("exit 0 over a version 1 flow.json, want non-zero")
	}
	if want := "run make flow in apps/old"; !strings.Contains(stderr, want) {
		t.Errorf("stderr %q, want it to say %q", stderr, want)
	}
	if _, err := os.Stat(filepath.Join(out, "graph.json")); !os.IsNotExist(err) {
		t.Errorf("graph.json written over a refused flow (stat: %v)", err)
	}
}

// Without -out the graph goes to <root>/.blueprint, where Steward
// reads it.
func TestTheGraphGoesUnderTheRootByDefault(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "mapping"))); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"-root", root}, io.Discard, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "graph.json")); err != nil {
		t.Errorf("no graph under the root: %v", err)
	}
}

// What the graph cannot be drawn without is refused by name, and an
// unknown flag is a usage error: a framework the graph would otherwise
// leave empty, a root holding no flow map (a -root pointing at the
// wrong directory), and a hop's rule no invariant in its flow map
// declares (an invariant node without its context or sentence).
func TestWhatTheGraphNeedsIsRefusedByName(t *testing.T) {
	t.Parallel()
	flowV2 := `{"version": 2, "entry_points": []}`
	for _, c := range []struct {
		name  string
		files map[string]string
		args  []string
		code  int
		says  string
	}{
		{"no config", map[string]string{"apps/a/docs/flow.json": flowV2}, nil, 1, ".blueprint.config.json"},
		{"no framework", map[string]string{".blueprint.config.json": `{}`, "apps/a/docs/flow.json": flowV2}, nil, 1, "names no framework"},
		{"no flow map", map[string]string{".blueprint.config.json": `{"framework": "non-ddd"}`}, nil, 1, "no apps/*/docs/flow.json under"},
		{"undeclared rule", map[string]string{".blueprint.config.json": `{"framework": "non-ddd"}`, "apps/a/docs/flow.json": `{"version": 2,
			"policies": [{"from": {"reason": "Paid", "context": "bank"}, "to": {"reason": "Thanked", "context": "notes"},
				"rules": [{"reason": "Thanked", "rule": "Nobody"}]}]}`}, nil, 1, "rule Nobody on Thanked names no declared invariant"},
		{"unknown flag", nil, []string{"-nope"}, 2, "flag provided but not defined"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for name, body := range c.files {
				p := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			code := run(append([]string{"-root", root, "-out", t.TempDir()}, c.args...), io.Discard, &stderr)
			if code != c.code || !strings.Contains(stderr.String(), c.says) {
				t.Errorf("exit %d, stderr %q; want exit %d saying %q", code, stderr.String(), c.code, c.says)
			}
		})
	}
}

// Edges are ordered by from, then kind, then to, whatever order they
// were found in. The golden's edges differ in from alone; this holds
// the two tie-breaks.
func TestEdgesSortByFromKindTo(t *testing.T) {
	t.Parallel()
	es := []edge{
		{From: "b", To: "a", Kind: "produces"},
		{From: "a", To: "z", Kind: "produces"},
		{From: "a", To: "y", Kind: "produces"},
		{From: "a", To: "z", Kind: "guarded-by"},
	}
	sortEdges(es)
	want := []edge{
		{From: "a", To: "z", Kind: "guarded-by"},
		{From: "a", To: "y", Kind: "produces"},
		{From: "a", To: "z", Kind: "produces"},
		{From: "b", To: "a", Kind: "produces"},
	}
	if !slices.Equal(es, want) {
		t.Errorf("sorted\n  got  %v\n  want %v", es, want)
	}
}

// readGraph runs the program over root and decodes the graph it wrote.
func readGraph(t *testing.T, root string) graph {
	t.Helper()
	code, stderr, out := runOver(t, root)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g graph
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// The scenarios of RS-20's forks F4, F6 and F11 and the design's edge
// table, each read off the mapping fixture's graph by id: the welcome
// policy between its two events, guarded by the rule its hop names;
// a written table; an agent invoking its entry point. No node is left
// without an edge.
func TestTheMappingDrawsEveryRow(t *testing.T) {
	t.Parallel()
	g := readGraph(t, filepath.Join("testdata", "mapping"))
	byID := map[string]node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	const welcome = "notes_pol_member-joined-then-welcome-note-written"
	if n, ok := byID[welcome]; !ok || n.Type != "policy" || n.Subdomain != "notes" {
		t.Errorf("node %s = %+v (present %v), want a policy in notes", welcome, n, ok)
	}
	for _, want := range []edge{
		{From: "accounts_evt_member-joined", To: welcome, Kind: "triggers"},
		{From: welcome, To: "notes_evt_welcome-note-written", Kind: "produces"},
		{From: "notes_evt_note-written", To: "notes_tbl_notes", Kind: "writes-to"},
		{From: welcome, To: "notes_inv_every-new-member-is-welcomed", Kind: "guarded-by"},
		{From: "bank_ext_bank", To: "bank_cmd_bank-reported-transaction", Kind: "invokes"},
	} {
		if !slices.Contains(g.Edges, want) {
			t.Errorf("no edge %s %s %s in %v", want.From, want.Kind, want.To, g.Edges)
		}
	}
	for _, n := range g.Nodes {
		if !slices.ContainsFunc(g.Edges, func(e edge) bool { return e.From == n.ID || e.To == n.ID }) {
			t.Errorf("orphan node %s", n.ID)
		}
	}
}

// A command is guarded by an invariant its entry's rules name even when
// that invariant declares no guard or shape over the command's reasons,
// and is guarded once however many clauses find it. A name the flow map
// gives no source for carries no meta.source rather than an empty one.
func TestAnEntrysRulesGuardItsCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".blueprint.config.json": `{"framework": "non-ddd"}`,
		"apps/a/docs/flow.json": `{"version": 2, "app": "a",
			"entry_points": [{"name": "Pay", "context": "bank", "reasons": ["Paid"], "actors": ["member"],
				"rules": [{"reason": "Paid", "rule": "PaidOnce"}, {"reason": "Paid", "rule": "Guarded"}]}],
			"invariants": [
				{"name": "PaidOnce", "context": "bank", "rule": "A bill is paid once.", "guards": [], "shapes": {}},
				{"name": "Guarded", "context": "bank", "rule": "Paid is guarded.", "guards": ["Paid"], "shapes": {}},
				{"name": "Shaped", "context": "bank", "rule": "Paid is shaped.", "guards": [], "shapes": {"Paid": ["bank_bills.amount"]}}]}`,
	})
	g := readGraph(t, root)
	want := []edge{
		{From: "bank_cmd_pay", To: "bank_inv_guarded", Kind: "guarded-by"},
		{From: "bank_cmd_pay", To: "bank_inv_paid-once", Kind: "guarded-by"},
		{From: "bank_cmd_pay", To: "bank_inv_shaped", Kind: "guarded-by"},
		{From: "bank_cmd_pay", To: "bank_evt_paid", Kind: "produces"},
	}
	if !slices.Equal(g.Edges, want) {
		t.Errorf("edges\n  got  %v\n  want %v", g.Edges, want)
	}
	for _, n := range g.Nodes {
		if n.Meta.Source != nil {
			t.Errorf("node %s has source %+v, the flow map gives none", n.ID, *n.Meta.Source)
		}
	}
}

// An agent that invokes several entry points in one context is one
// external system, whose source is its first entry point's.
func TestAnAgentsSourceIsItsFirstEntryPoint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".blueprint.config.json": `{"framework": "non-ddd"}`,
		"apps/a/docs/flow.json": `{"version": 2, "app": "a",
			"entry_points": [
				{"name": "Pay", "context": "bank", "reasons": ["Paid"], "actors": ["agent:bank"], "rules": []},
				{"name": "Refund", "context": "bank", "reasons": ["Refunded"], "actors": ["agent:bank"], "rules": []}],
			"sources": {"entry_points": {
				"Pay": {"file": "internal/a/pay.go", "line": 10},
				"Refund": {"file": "internal/a/refund.go", "line": 20}}}}`,
	})
	g := readGraph(t, root)
	want := source{File: "apps/a/internal/a/pay.go", Line: 10}
	found := false
	for _, n := range g.Nodes {
		if n.Type != "external-system" {
			continue
		}
		found = true
		if n.ID != "bank_ext_bank" {
			t.Errorf("external system id %s, want bank_ext_bank", n.ID)
		}
		if n.Meta.Source == nil || *n.Meta.Source != want {
			t.Errorf("external system %s source %+v, want %+v", n.ID, n.Meta.Source, want)
		}
	}
	if !found {
		t.Errorf("no external-system node in %+v", g.Nodes)
	}
}

// writeTree writes each file, by slash path under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
