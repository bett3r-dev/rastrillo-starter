//go:build perf

package myapptest

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/perf/perftest"

	myapp "myapp/internal/myapp"
)

// The perf lane: make perf, serial and uncached, failing only on CI.
// Every GET screen the router serves is held to 150ms to first byte; a
// new route fails here until it is measured or has a record in
// .rastrillo/budgets.txt. Never t.Parallel() in this file: a perf test
// sharing the machine measures its neighbours.
func TestPerfScreens(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(schema.Path(t), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	r, err := myapp.Router(d, testOrigin, logger)
	if err != nil {
		t.Fatal(err)
	}
	opts := rastrillo.Options{Logger: logger}
	myapp.Configure(&opts, myapp.Mux(r), time.Now())
	h, closeAll, err := rastrillo.Handler(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	// Screens behind sign-in need Signin; a {param} route needs a Paths
	// entry whose row this test seeds first.
	// Every screen but /signin and /signup is behind sign-in: the
	// client joins first, as a member would, and writes note 1 so the
	// correction screen is a real one.
	perftest.Screens(t, perftest.ScreenConfig{Routes: r, Handler: h, Signin: joinAs("Perf"),
		Paths: map[string]string{"/notes/{id}/correct": "/notes/1/correct"}})
}

// A cold start, from exec to the first byte of "/", held to 500ms.
func TestPerfBoot(t *testing.T) {
	perftest.Boot(t, perftest.BootConfig{
		Prepare: schema.Path,
		Build: func(path string) (http.Handler, func(), error) {
			started := time.Now()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			d, err := db.Open(path, logger)
			if err != nil {
				return nil, nil, err
			}
			mux, err := myapp.App(d, testOrigin, logger)
			if err != nil {
				d.Close()
				return nil, nil, err
			}
			opts := rastrillo.Options{Logger: logger}
			myapp.Configure(&opts, mux, started)
			h, closeAll, err := rastrillo.Handler(opts)
			if err != nil {
				d.Close()
				return nil, nil, err
			}
			return h, func() { closeAll(); d.Close() }, nil
		},
		Path: "/signin", // the public page; "/" is behind sign-in
	})
}

// joinAs signs the perf client in by joining through the real form,
// with the Origin header csrf.Protect requires, then writes a note.
func joinAs(name string) func(c *http.Client, base string) {
	return func(c *http.Client, base string) {
		send(c, base+"/signup", url.Values{"name": {name}, "email": {strings.ToLower(name) + "@example.test"}, "password": {"correct horse battery"}})
		send(c, base+"/notes", url.Values{"title": {"First"}, "body": {"Written by the perf lane."}})
	}
}

func send(c *http.Client, target string, form url.Values) {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// The same-origin evidence is the app's configured origin, not
	// the listener's address: csrf.Protect compares against the
	// former.
	req.Header.Set("Origin", testOrigin)
	res, err := c.Do(req)
	if err != nil {
		panic(err)
	}
	res.Body.Close()
}
