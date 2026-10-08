//go:build perf

package myapptest

import (
	"io"
	"log/slog"
	"net/http"
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
	perftest.Screens(t, perftest.ScreenConfig{Routes: r, Handler: h})
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
		Path: "/",
	})
}
