// Command myapp wires the app: resolve the platform's activation
// argv, open the database, build the app, serve.
package main

import (
	"log/slog"
	"os"
	"time"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/db"

	myapp "myapp/internal/myapp"
)

func main() {
	// First, so perf's cold budget counts the whole start: a hibernated
	// instance pays all of it on someone's click.
	started := time.Now()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Resolve, not Run: this app opens its own database handle via
	// db.Open — a *gorm.DB with a split reader/writer pool, not the
	// bare *sql.DB Options.Router would hand back — so Options.DBPath
	// must be empty again by the Serve call below, or Serve would open
	// a second, unused connection to the same file.
	opts, err := rastrillo.Resolve(rastrillo.Options{DBPath: "myapp.db", Logger: logger})
	if err != nil {
		logger.Error("resolve activation", "err", err)
		os.Exit(1)
	}

	d, err := db.Open(opts.DBPath, logger)
	if err != nil {
		logger.Error("open database", "err", err)
		os.Exit(1)
	}
	defer d.Close()

	// Origin decides the CSRF same-origin check (and, when the app
	// grows accounts, the Secure/__Host- cookie attributes) — so
	// defaulting it silently in production would be a real bug. Loud
	// on purpose, harmless in local dev.
	origin := os.Getenv("MYAPP_ORIGIN")
	if origin == "" {
		origin = "http://localhost:8080"
		logger.Warn("MYAPP_ORIGIN not set; defaulting", "origin", origin)
	}

	mux, err := myapp.App(d, origin, logger)
	if err != nil {
		logger.Error("build app", "err", err)
		os.Exit(1)
	}

	// Configure sets Mux, ErrorPage (wire render.go's ErrorPage to
	// Ctx.ErrorPage too as the app grows handlers) and perf's timing,
	// the same options the perf test measures through.
	myapp.Configure(&opts, mux, started)
	opts.DBPath = ""
	if err := rastrillo.Serve(opts); err != nil {
		logger.Error("serve failed", "err", err)
		os.Exit(1)
	}
}
