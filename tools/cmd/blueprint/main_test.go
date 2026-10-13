package main

import (
	"bytes"
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

// A two-context v2 flow.json becomes the graph written by hand in
// want-graph.json from design.md's mapping (entry point → command,
// reason → event, context → subdomain, command produces event; ids by
// NodeKey; nodes by id, edges by from, kind, to; framework from the
// root .blueprint.config.json): byte for byte, so the pretty print,
// the trailing newline and the absence of a timestamp are held too.
// NoteWritten, landed by two entry points, is one node.
func TestAV2FlowBecomesItsGraph(t *testing.T) {
	t.Parallel()
	root := filepath.Join("testdata", "two-contexts")
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
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "two-contexts"))); err != nil {
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
// leave empty, and a root holding no flow map (a -root pointing at the
// wrong directory).
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
