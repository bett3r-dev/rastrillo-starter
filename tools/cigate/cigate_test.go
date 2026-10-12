// Package cigate tests scripts/ci-gate, the runner every Makefile's ci
// line goes through: it certifies only what ran, so a target that
// cannot run says SKIP and is listed as skipped, and a target whose run
// was removed without a SKIP line fails the gate.
package cigate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

// makefile holds one target of each kind the gate meets.
const makefile = `
.PHONY: ran skipper nested empty silentfail
ran:
	@echo running
skipper:
	@echo "SKIP skipper reason=no toolchain here"
nested:
	@echo "SKIP inner reason=an app's gate skipped it"
empty:
silentfail:
	@exit 3
`

// gate runs scripts/ci-gate over targets in a directory holding
// makefile, and returns its output and exit code.
func gate(t *testing.T, targets ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-gate"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script, targets...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MAKEFLAGS=", "MAKELEVEL=")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestTheGateEndsWithEverySkipItSaw(t *testing.T) {
	t.Parallel()
	out, code := gate(t, "ran", "skipper", "nested")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	tail := out[strings.LastIndex(out, "== ci summary"):]
	for _, want := range []string{
		"== ci summary: 2 ran, 2 skipped",
		"CI-SKIPPED skipper reason=no toolchain here",
		"CI-SKIPPED inner reason=an app's gate skipped it (in nested)",
		"CI: PASS on what ran; 2 SKIPPED, NOT PASSED",
	} {
		if !strings.Contains(tail, want) {
			t.Errorf("summary lacks %q:\n%s", want, tail)
		}
	}
}

func TestEveryTargetRanIsAPass(t *testing.T) {
	t.Parallel()
	out, code := gate(t, "ran")
	if code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "CI: PASS, every target ran") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// A target whose run was removed, and which says no SKIP, fails the
// gate by name, and nothing after it runs.
func TestATargetThatRunsNothingFailsTheGate(t *testing.T) {
	t.Parallel()
	out, code := gate(t, "empty", "ran")
	if code == 0 || !strings.Contains(out, "CI: FAIL at empty: it runs nothing and printed no 'SKIP empty reason=<why>' line") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "running") {
		t.Fatalf("the gate went on after a failure:\n%s", out)
	}
}

func TestAFailingTargetFailsTheGate(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"silentfail", "nosuchtarget"} {
		out, code := gate(t, "ran", target)
		if code == 0 || !strings.Contains(out, "CI: FAIL at "+target) {
			t.Errorf("%s: exit %d:\n%s", target, code, out)
		}
	}
}
