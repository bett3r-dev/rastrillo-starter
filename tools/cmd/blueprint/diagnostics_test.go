package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// golden runs the program over a fixture and compares the
// diagnostics.json it wrote with the fixture's want-diagnostics.json,
// byte for byte: every one of the twelve arrays present, empty or not.
// It returns the exit code, the stderr and the output directory.
func golden(t *testing.T, root string) (int, string, string) {
	t.Helper()
	code, stderr, out := runOver(t, root)
	got, err := os.ReadFile(filepath.Join(out, "diagnostics.json"))
	if err != nil {
		t.Fatalf("exit %d, stderr %q: %v", code, stderr, err)
	}
	want, err := os.ReadFile(filepath.Join(root, "want-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("diagnostics.json\n--- got\n%s\n--- want\n%s", got, want)
	}
	return code, stderr, out
}

// readDiagnostics decodes the diagnostics.json a run wrote.
func readDiagnostics(t *testing.T, out string) diagnostics {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, "diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d diagnostics
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// The mapping fixture draws only legal edges, leaves no node alone and
// lands NoteWritten under two entry points of one app: one node, so no
// name collision. Its diagnostics carry pv3's eight arrays and the four
// flow findings, every one present and empty, and the run passes.
func TestACleanMapReportsEveryArrayEmpty(t *testing.T) {
	t.Parallel()
	if code, stderr, _ := golden(t, filepath.Join("testdata", "mapping")); code != 0 {
		t.Errorf("exit %d, stderr %q, want 0", code, stderr)
	}
}

// RS-20-F3 "Two boxes, the smell visible": Join landing in accounts and
// in notes is a command in each, and mixedContextRequests names Join.
// RS-20-F5 "Reported only": an invariant nothing guards is an orphan,
// and the run still exits 0. The flow map's unnamed entry points,
// changes with no entry point and implicit crossings are carried over,
// each with its app.
func TestTheFlowFindingsAreReportedNotFailed(t *testing.T) {
	t.Parallel()
	root := filepath.Join("testdata", "findings")
	code, stderr, out := golden(t, root)
	if code != 0 {
		t.Errorf("exit %d, stderr %q, want 0: an orphan and the flow findings are reported only", code, stderr)
	}
	d := readDiagnostics(t, out)
	if !slices.ContainsFunc(d.MixedContextRequests, func(m mixed) bool { return m.EntryPoint == "Join" }) {
		t.Errorf("mixedContextRequests %+v, want it to name Join", d.MixedContextRequests)
	}
	if want := (orphan{ID: "accounts_inv_nobody-is-turned-away", Type: "invariant"}); !slices.Contains(d.OrphanNodes, want) {
		t.Errorf("orphanNodes %+v, want %+v", d.OrphanNodes, want)
	}
	g := readGraph(t, root)
	for _, id := range []string{"accounts_cmd_join", "notes_cmd_join"} {
		if !slices.ContainsFunc(g.Nodes, func(n node) bool { return n.ID == id && n.Type == "command" }) {
			t.Errorf("no command %s in the graph", id)
		}
	}
}

// RS-20-F2 "Ambiguity surfaced": apps a and b both declare accounts.
// diagnostics.json lists accounts under nameCollisions, a site per
// app's flow map, the graph is still written, and the run exits 1,
// saying why.
func TestAContextTwoAppsDeclareIsACollision(t *testing.T) {
	t.Parallel()
	code, stderr, out := golden(t, filepath.Join("testdata", "clash"))
	if code != 1 {
		t.Errorf("exit %d, want 1: two apps declare accounts", code)
	}
	if want := "0 topology violation(s) and 1 name collision(s)"; !strings.Contains(stderr, want) {
		t.Errorf("stderr %q, want it to say %q", stderr, want)
	}
	if _, err := os.Stat(filepath.Join(out, "graph.json")); err != nil {
		t.Errorf("no graph.json beside the diagnostics: %v", err)
	}
}

// Two names one app gives one id (WriteNote and Write_Note both slug to
// write-note) are a collision too, a site per name: their declaration,
// or the app's flow map where it gives none. Collisions are listed by
// name, whichever kind: the id before zeta, the context apps a and b
// both declare. The run exits 1.
func TestTwoNamesForOneIdAreACollision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	zeta := `{"name": "Tick", "context": "zeta", "reasons": ["Ticked"], "actors": ["member"], "rules": []}`
	writeTree(t, root, map[string]string{
		".blueprint.config.json": `{"framework": "non-ddd"}`,
		"apps/a/docs/flow.json": `{"version": 2, "app": "a",
			"entry_points": [
				{"name": "WriteNote", "context": "notes", "reasons": ["NoteWritten"], "actors": ["member"], "rules": []},
				{"name": "Write_Note", "context": "notes", "reasons": ["NoteWritten"], "actors": ["member"], "rules": []},
				` + zeta + `],
			"sources": {"entry_points": {"WriteNote": {"file": "internal/a/handlers.go", "line": 7}}}}`,
		"apps/b/docs/flow.json": `{"version": 2, "app": "b", "entry_points": [` + zeta + `]}`,
	})
	code, stderr, out := runOver(t, root)
	if code != 1 {
		t.Errorf("exit %d (stderr %q), want 1: two names share an id", code, stderr)
	}
	want := []collision{
		{Name: "notes_cmd_write-note", Type: "command", Sites: []site{
			{File: "apps/a/internal/a/handlers.go", Line: 7},
			{File: "apps/a/docs/flow.json", Line: 1},
		}},
		{Name: "zeta", Type: "subdomain", Sites: []site{
			{File: "apps/a/docs/flow.json", Line: 1},
			{File: "apps/b/docs/flow.json", Line: 1},
		}},
	}
	if got := readDiagnostics(t, out).NameCollisions; !equalJSON(got, want) {
		t.Errorf("nameCollisions\n  got  %+v\n  want %+v", got, want)
	}
}

// RS-20-F5 "Tool defect fails": the extractor cannot draw event produces
// event from a valid flow map, so the graph is handed to report, the
// step every run whose input was read ends in, as a value. The edge is named in
// topologyViolations and left out of graph.json, an edge to no node is
// a violation too (its missing type empty), the event it alone touched
// is an orphan, and the run exits 1.
func TestAnIllegalEdgeFailsTheRun(t *testing.T) {
	t.Parallel()
	g := graph{SchemaVersion: 1, Framework: "non-ddd", Subdomains: []string{"notes"},
		Nodes: []node{
			{ID: "notes_cmd_write-note", Type: "command", Subdomain: "notes"},
			{ID: "notes_evt_note-corrected", Type: "event", Subdomain: "notes"},
			{ID: "notes_evt_note-written", Type: "event", Subdomain: "notes"},
		},
		Edges: []edge{
			{From: "notes_cmd_write-note", To: "notes_evt_note-written", Kind: "produces"},
			{From: "notes_cmd_write-note", To: "notes_evt_ghost", Kind: "produces"},
			{From: "notes_evt_note-corrected", To: "notes_evt_note-written", Kind: "produces"},
		}}
	out := t.TempDir()
	var stderr bytes.Buffer
	code := report(out, g, newDiagnostics(), &stderr)
	if code != 1 {
		t.Errorf("exit %d, want 1 over event produces event", code)
	}
	if want := "2 topology violation(s) and 0 name collision(s)"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr %q, want it to say %q", stderr.String(), want)
	}
	d := readDiagnostics(t, out)
	wantV := []violation{
		{From: "notes_cmd_write-note", To: "notes_evt_ghost", Kind: "produces", FromType: "command", ToType: ""},
		{From: "notes_evt_note-corrected", To: "notes_evt_note-written", Kind: "produces", FromType: "event", ToType: "event"},
	}
	if !slices.Equal(d.TopologyViolations, wantV) {
		t.Errorf("topologyViolations\n  got  %+v\n  want %+v", d.TopologyViolations, wantV)
	}
	if want := []orphan{{ID: "notes_evt_note-corrected", Type: "event"}}; !slices.Equal(d.OrphanNodes, want) {
		t.Errorf("orphanNodes %+v, want %+v", d.OrphanNodes, want)
	}
	data, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written graph
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	if want := []edge{{From: "notes_cmd_write-note", To: "notes_evt_note-written", Kind: "produces"}}; !slices.Equal(written.Edges, want) {
		t.Errorf("graph.json edges %v, want only the legal %v", written.Edges, want)
	}
}

// The grammar is design.md's nonDDDRules list, exactly and in its
// order: the list below is a frozen copy of the design/vocabulary.go
// bullet in design.md's blueprint-go section, so a change to
// nonDDDRules shows here and a change to design.md does not. Drift
// between the two copies is guarded by slice 10's contract fixture.
func TestTheGrammarIsTheDesignsList(t *testing.T) {
	t.Parallel()
	design := "`command produces event`, `command guarded-by invariant`, `event triggers policy`, " +
		"`policy produces event`, `policy guarded-by invariant`, `external-system invokes command`, " +
		"`event writes-to datastore`, `ui invokes command`"
	var want []rule
	for _, item := range strings.Split(design, ", ") {
		f := strings.Fields(strings.Trim(item, "`"))
		want = append(want, rule{From: f[0], Kind: f[1], To: f[2]})
	}
	if !slices.Equal(nonDDDRules, want) {
		t.Errorf("nonDDDRules\n  got  %v\n  want %v", nonDDDRules, want)
	}
}

// Both files are written whole or not at all: each goes to a temporary
// sibling renamed over the old one, so a reader never sees half a file.
// The rename is what replaces a read-only leftover a plain write could
// not open; nothing but the two files is left behind, each 0644.
func TestBothFilesAreReplacedWhole(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	for _, name := range []string{"graph.json", "diagnostics.json"} {
		if err := os.WriteFile(filepath.Join(out, name), []byte("stale"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	var stderr bytes.Buffer
	if code := run([]string{"-root", filepath.Join("testdata", "mapping"), "-out", out}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v, want 0644", e.Name(), info.Mode().Perm())
		}
		if data, _ := os.ReadFile(filepath.Join(out, e.Name())); string(data) == "stale" {
			t.Errorf("%s still stale", e.Name())
		}
	}
	if want := []string{"diagnostics.json", "graph.json"}; !slices.Equal(names, want) {
		t.Errorf("out holds %v, want %v", names, want)
	}
}

// equalJSON compares two values by their JSON.
func equalJSON(a, b any) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}
