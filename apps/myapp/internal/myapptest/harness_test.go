// Package myapptest tests the app from the outside: real HTTP
// requests against the real router, wired exactly as
// cmd/myapp/main.go wires it.
//
// The TDD loop: write a failing test here, make it pass in
// internal/myapp, repeat.
package myapptest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/dbtest"

	myapp "myapp/internal/myapp"
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
	schema.Remove()
	os.Exit(code)
}

// newApp builds the whole app per test over its own copy of the
// migrated database, exactly as main.go does.
func newApp(t *testing.T) (http.Handler, *db.DB) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(schema.Path(t), logger)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	mux, err := myapp.App(d, testOrigin, logger)
	if err != nil {
		t.Fatalf("App: %v", err)
	}
	return mux, d
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
