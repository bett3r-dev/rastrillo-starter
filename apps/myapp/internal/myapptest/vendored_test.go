package myapptest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"amadan.net/rastrillo/rastrillo/ui"
)

// vendoredTheme is the theme this app was scaffolded with. Change it
// here when you swap static/theme.css for another of ui.ThemeNames().
const vendoredTheme = "day"

// vendoredIsMine names the vendored files this app has edited
// DELIBERATELY. Add one here when you mean to diverge: this test stops
// pinning it, and rastrillo doctor leaves it alone too. The file was
// yours from the moment the scaffold wrote it — this is only where you
// record that the edit was on purpose, so the next reader (and the next
// upgrade) can tell it from drift.
var vendoredIsMine = map[string]bool{
	// "tokens.css": true,
	//
	// Only the sidebar and console shells link shell.js and shell.css.
	// An app on another shell that deletes them records it here, or this
	// test fails on the missing files:
	// "shell.js": true, "shell.css": true,
}

// The scaffold delivered these files once; they are app-owned from
// then on. This test pins each vendored copy byte-identical to the
// library it came from: a reviewer runs the suite instead of reading
// ~56KB of assets as app diff, and a framework upgrade that forgets
// to re-copy is caught instead of drifting silently.
//
// The list is ui.VendoredAssets — the same function rastrillo new
// wrote these files from and rastrillo doctor compares them against —
// so a file added to the library's vendored set is pinned here without
// this file changing.
func TestVendoredAssetsMatchTheLibrary(t *testing.T) {
	t.Parallel()
	assets, ok := ui.VendoredAssets(vendoredTheme)
	if !ok {
		t.Fatalf("unknown theme %q", vendoredTheme)
	}
	for name, lib := range assets {
		if vendoredIsMine[name] {
			continue
		}
		vendored, err := os.ReadFile(filepath.Join("..", "myapp", "static", name))
		if err != nil {
			t.Errorf("read vendored %s: %v", name, err)
			continue
		}
		if !bytes.Equal(vendored, lib) {
			t.Errorf("static/%s differs from the library copy; re-copy it with rastrillo doctor --fix (or add it to vendoredIsMine if the edit was deliberate)", name)
		}
	}
}
