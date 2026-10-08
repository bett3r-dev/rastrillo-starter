//go:build browser

// The browser drive: the whole app in a real Chromium, wired exactly
// as cmd/myapp/main.go wires it — loud on any console error, failed
// request, 4xx/5xx, or JS value that leaked to the screen unrendered.
// Not part of the plain suite:
//
//	go test -tags browser ./...
//
// It needs a Chromium: on PATH, via RASTRILLO_CHROME, or in a
// Playwright cache. A skip is not a pass — with no browser this fails,
// unless RASTRILLO_BROWSER_OPTIONAL is set, which makes the skip a
// deliberate visible choice rather than an accident.
package myapptest

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/harness"
	"github.com/chromedp/chromedp"

	myapp "myapp/internal/myapp"
)

// TestBrowserWalk is the minimal loud walk: the home screen arrives
// with nothing wrong anywhere the rig watches. Grow it with the app —
// each screen a flow reaches earns a Screen() line, and an expected
// probe (a signed-out 401, say) earns a rig.Allow.
func TestBrowserWalk(t *testing.T) {
	rig := harness.New(t, func(origin string) http.Handler {
		// The scaffold's own wiring, origin included: the rig hands
		// the app its localhost origin before serving — the same seam
		// main.go fills from MYAPP_ORIGIN.
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		d, err := db.Open(filepath.Join(t.TempDir(), "app.db"), logger)
		if err != nil {
			t.Fatalf("db.Open: %v", err)
		}
		t.Cleanup(func() { d.Close() })
		mux, err := myapp.App(d, origin, logger)
		if err != nil {
			t.Fatalf("App: %v", err)
		}
		return mux
	})
	rig.Run(chromedp.Navigate(rig.Origin + "/"))
	rig.Screen("body", "home")
}
