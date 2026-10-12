// Package myapptest tests the app from the outside: real HTTP
// requests against the real router, wired exactly as
// cmd/myapp/main.go wires it.
//
// The TDD loop: write a failing test here, make it pass in
// internal/myapp, repeat.
package myapptest

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/dbtest"

	myapp "myapp/internal/myapp"
	"myproject/libs/telemetry"
)

// testOrigin is the origin newApp wires and post presents as its
// same-origin evidence — the pair a browser form submission would be.
const testOrigin = "http://app.test"

// schema is migrated once per test binary and copied for each test:
// migrating a fresh database in every test is the cost Tito Go measured
// at 207ms a test against 2.5ms for a copy.
var schema = dbtest.FromSet(myapp.BootSchema)

// TestMain prints this package's time for rastrillo budget test (make
// test), then removes the template, which outlives every test that
// copied it.
func TestMain(m *testing.M) {
	code := budget.Main(m)
	if code == 0 {
		code = flowGate() // only under make flow / make flow-check (flow_test.go)
	}
	if code == 0 {
		code = routeGate() // every mounted route reached by a scenario (coverage_test.go)
	}
	if code == 0 {
		code = writeGate() // every write in the source run by a scenario (writesites_test.go)
	}
	schema.Remove()
	os.Exit(code)
}

// newApp builds the whole app per test over its own copy of the
// migrated database, exactly as main.go does, and hands back the
// database too so a Then can read the change records. A scenario's
// handler also records the routes it reaches (coverage_test.go).
func newApp(t *testing.T) (http.Handler, *db.DB) {
	t.Helper()
	// Errors reach the test's own output (shown when it fails), with
	// the trace id as main.go's logger puts it on every line: a refused
	// write's 500 then names the rule, the reason, the table and the
	// request, not only the status.
	tl := &testLog{t: t}
	t.Cleanup(func() { tl.mu.Lock(); tl.done = true; tl.mu.Unlock() })
	logger := slog.New(telemetry.Correlate(slog.NewTextHandler(tl, &slog.HandlerOptions{Level: slog.LevelError})))
	d, err := db.Open(schema.Path(t), logger)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	recordWrites(t, d.G)
	r, err := myapp.Router(d, testOrigin, logger)
	if err != nil {
		t.Fatalf("Router: %v", err)
	}
	return coverRoutes(t, r, myapp.Mux(r)), d
}

// testLog writes log lines to t.Log until the test ends; a line from
// a goroutine that outlives it goes nowhere, since t.Log would panic.
type testLog struct {
	t    *testing.T
	mu   sync.Mutex
	done bool
}

func (l *testLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.done {
		l.t.Log(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// post is here before anything calls it: the first form test you write
// needs the Origin header below, and nothing about a 403 says so.
//
//lint:ignore U1000 kept for the app's first form test; see above
func post(t *testing.T, h http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The same-origin evidence csrf.Protect requires — every current
	// browser sends it on a form POST; a test client must too.
	req.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
