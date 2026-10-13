package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the repository root, three directories above this
// package (tools/cmd/blueprint).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The program over the repository itself, as blueprint-check runs it:
// the committed .blueprint.config.json and every committed
// apps/*/docs/flow.json, into a temporary directory. myapp's graph is
// read off apps/myapp/docs/flow.json by hand, ids as design.md's
// NodeKey spells them: three commands, four events, the welcome policy,
// two invariants and two tables, eleven edges, and no diagnostic that
// fails the run. Only flow.json is read: no scenario runs.
func TestTheRepositorysFlowMapsDrawMyapp(t *testing.T) {
	t.Parallel()
	code, stderr, out := runOver(t, repoRoot(t))
	if code != 0 {
		t.Fatalf("exit %d over the repository, want 0; stderr %q", code, stderr)
	}
	var g graph
	readJSON(t, filepath.Join(out, "graph.json"), &g)
	if g.Framework != "non-ddd" {
		t.Errorf("framework %q, want %q", g.Framework, "non-ddd")
	}
	if want := []string{"accounts", "notes"}; !slices.Equal(g.Subdomains, want) {
		t.Errorf("subdomains %v, want %v", g.Subdomains, want)
	}
	const welcome = "notes_pol_member-joined-then-welcome-note-written"
	wantNodes := map[string]string{
		"accounts_cmd_join":                        "command",
		"accounts_evt_member-joined":               "event",
		"accounts_tbl_members":                     "datastore",
		"notes_cmd_correct-note":                   "command",
		"notes_cmd_write-note":                     "command",
		"notes_evt_note-corrected":                 "event",
		"notes_evt_note-written":                   "event",
		"notes_evt_welcome-note-written":           "event",
		"notes_inv_a-correction-changes-something": "invariant",
		"notes_inv_every-new-member-is-welcomed":   "invariant",
		welcome:                                    "policy",
		"notes_tbl_notes":                          "datastore",
	}
	gotNodes := map[string]string{}
	for _, n := range g.Nodes {
		gotNodes[n.ID] = n.Type
		if n.Meta.App != "myapp" {
			t.Errorf("node %s meta.app %q, want myapp", n.ID, n.Meta.App)
		}
	}
	if fmt.Sprint(gotNodes) != fmt.Sprint(wantNodes) {
		t.Errorf("nodes\n  got  %v\n  want %v", gotNodes, wantNodes)
	}
	wantEdges := []edge{
		{From: "accounts_cmd_join", To: "accounts_evt_member-joined", Kind: "produces"},
		{From: "accounts_evt_member-joined", To: welcome, Kind: "triggers"},
		{From: "accounts_evt_member-joined", To: "accounts_tbl_members", Kind: "writes-to"},
		{From: "notes_cmd_correct-note", To: "notes_inv_a-correction-changes-something", Kind: "guarded-by"},
		{From: "notes_cmd_correct-note", To: "notes_evt_note-corrected", Kind: "produces"},
		{From: "notes_cmd_write-note", To: "notes_evt_note-written", Kind: "produces"},
		{From: "notes_evt_note-corrected", To: "notes_tbl_notes", Kind: "writes-to"},
		{From: "notes_evt_note-written", To: "notes_tbl_notes", Kind: "writes-to"},
		{From: "notes_evt_welcome-note-written", To: "notes_tbl_notes", Kind: "writes-to"},
		{From: welcome, To: "notes_inv_every-new-member-is-welcomed", Kind: "guarded-by"},
		{From: welcome, To: "notes_evt_welcome-note-written", Kind: "produces"},
	}
	if !slices.Equal(g.Edges, wantEdges) {
		t.Errorf("edges\n  got  %v\n  want %v", g.Edges, wantEdges)
	}
	var d diagnostics
	readJSON(t, filepath.Join(out, "diagnostics.json"), &d)
	if len(d.TopologyViolations)+len(d.NameCollisions)+len(d.OrphanNodes) != 0 {
		t.Errorf("topologyViolations %v, nameCollisions %v, orphanNodes %v; want all empty",
			d.TopologyViolations, d.NameCollisions, d.OrphanNodes)
	}
}

// readJSON decodes the file at path into v.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// artifactsConfigured says what is wrong with the repository's
// Blueprint artifacts (design.md fork F10): .gitignore must ignore
// .blueprint/, the generated graph, on a line of its own, and
// .blueprint.config.json must say framework non-ddd and name make
// blueprint as designTooling.extract, the command Steward runs.
func artifactsConfigured(gitignore, config []byte) error {
	ignored := false
	sc := bufio.NewScanner(bytes.NewReader(gitignore))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == ".blueprint/" {
			ignored = true
		}
	}
	if !ignored {
		return fmt.Errorf(".gitignore has no .blueprint/ line")
	}
	var cfg struct {
		Framework     string `json:"framework"`
		DesignTooling struct {
			Extract string `json:"extract"`
		} `json:"designTooling"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf(".blueprint.config.json: %w", err)
	}
	if cfg.Framework != "non-ddd" {
		return fmt.Errorf(".blueprint.config.json framework %q, want %q", cfg.Framework, "non-ddd")
	}
	if cfg.DesignTooling.Extract != "make blueprint" {
		return fmt.Errorf(".blueprint.config.json designTooling.extract %q, want %q", cfg.DesignTooling.Extract, "make blueprint")
	}
	return nil
}

// The repository's .gitignore and .blueprint.config.json pass; each
// missing or wrong piece, in a copy, fails by name (a commented-out
// entry is no entry).
func TestTheBlueprintArtifactsAreConfigured(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(root, ".blueprint.config.json"))
	if err != nil {
		t.Fatalf("no committed .blueprint.config.json: %v", err)
	}
	if err := artifactsConfigured(gitignore, config); err != nil {
		t.Errorf("the repository's artifacts: %v", err)
	}
	good := `{"framework": "non-ddd", "designTooling": {"extract": "make blueprint"}}`
	for _, c := range []struct{ name, gitignore, config, says string }{
		{"no ignore", ".work/\n", good, "no .blueprint/ line"},
		{"commented ignore", "# .blueprint/\n", good, "no .blueprint/ line"},
		{"ddd", ".blueprint/\n", `{"framework": "ddd", "designTooling": {"extract": "make blueprint"}}`, `framework "ddd"`},
		{"no extract", ".blueprint/\n", `{"framework": "non-ddd"}`, `designTooling.extract ""`},
	} {
		err := artifactsConfigured([]byte(c.gitignore), []byte(c.config))
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, want an error saying %q", c.name, err, c.says)
		}
	}
	if err := artifactsConfigured([]byte(".work/\n.blueprint/\n"), []byte(good)); err != nil {
		t.Errorf("a well-formed pair: %v", err)
	}
}

// ciTargets is the root Makefile's CI list, the targets
// scripts/ci-gate runs: the words after "CI :=".
func ciTargets(makefile []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(makefile))
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "CI := "); ok {
			return strings.Fields(rest)
		}
	}
	return nil
}

// The root make ci runs blueprint-check (design.md: "The root make ci
// gains blueprint-check"); a Makefile whose CI list lacks it fails.
func TestTheRootGateRunsBlueprintCheck(t *testing.T) {
	t.Parallel()
	makefile, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ciTargets(makefile); !slices.Contains(got, "blueprint-check") {
		t.Errorf("the root Makefile's CI list %v has no blueprint-check", got)
	}
	if got := ciTargets([]byte("CI := toolchain-check shared apps gitleaks\n")); slices.Contains(got, "blueprint-check") {
		t.Errorf("a CI list without blueprint-check read as %v", got)
	}
}
