package myapp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/csrf"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/migrate"
	"amadan.net/rastrillo/rastrillo/perf"
)

// App wires the whole app: schema, router, static files. It returns a
// *http.ServeMux because rastrillo.Options.Mux is typed that way — the
// chi router mounts inside it. Router and Mux are its two halves,
// separate only so the perf test can walk the chi routes while measuring
// through the same handler production serves. origin is the app's external origin
// ("https://app.example.com") — the CSRF check's yardstick, and, once
// the app grows accounts, sessions.Config.Origin too.
//
// Growing the app is SKILL.md's five-file shape: models in models.go,
// handlers in handlers.go, and — for a multi-user app — the sessions
// core plus an identity plugin wired right here (examples/notes is the
// worked example to copy — it lives in the repository at
// amadan.net/rastrillo/rastrillo, not in the module you depend on, so
// read it there rather than looking for the directory). Adding a subsystem
// also means adding its Schema to migrations.go's BootSchema — see
// the comment there. Past 5,000 lines here, split by feature: a package
// per feature under internal/myapp/, each with Mount(r chi.Router, deps)
// called from Router (docs/site/testing.md).
func App(d *db.DB, origin string, logger *slog.Logger) (*http.ServeMux, error) {
	r, err := Router(d, origin, logger)
	if err != nil {
		return nil, err
	}
	return Mux(r), nil
}

// Router applies the schema and builds the routes.
func Router(d *db.DB, origin string, logger *slog.Logger) (chi.Router, error) {
	// Apply BootSchema, not Schema: BootSchema is everything this
	// app needs at boot (its own migrations plus any subsystem's),
	// while Schema (migrations.go) stays just this app's own so the
	// generator and its check never see a subsystem's tables and try
	// to drop them.
	migrated, err := migrate.Apply(context.Background(), d, BootSchema)
	if err != nil {
		return nil, err
	}
	// One line per boot. Result exists so this can be logged, and
	// until something does, a fleet-wide adoption — every database
	// stamped, no DDL run, exactly once, never again — produces no
	// output at all, which is the last event you want to be silent.
	logger.Info("migrate", "applied", migrated.Applied,
		"skipped", migrated.Skipped, "adopted", migrated.Adopted)

	a := &app{db: d, logger: logger}

	r := chi.NewRouter()
	// App-wide, from day one: every state-changing route added below
	// is born covered. Origin-checking, not tokens — nothing to mint
	// or forget in a form.
	r.Use(csrf.Protect(origin))
	r.Get("/", a.index)
	return r, nil
}

// Mux mounts the static files beside the router.
func Mux(r http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	// The app serves its own static files — the framework never does.
	// They are embedded (render.go) and fingerprinted: templates link
	// them via {{asset ...}}, which returns a content-hashed URL the
	// handler serves cacheable-forever.
	mux.Handle("GET /static/", assets.Handler())
	mux.Handle("/", r)
	return mux
}

// Configure is the one place the serving options are set, so main.go and
// the perf test cannot drift apart: what the perf lane measures is what
// production serves.
//
// ErrorPage is the page a handler's own error path renders, so a panic
// and a handled failure look identical. perf.Middleware times every
// request against 150ms to first byte (500ms for the first one after
// start, counted from started) and logs what is over budget.
func Configure(opts *rastrillo.Options, mux *http.ServeMux, started time.Time) {
	opts.Mux = mux
	opts.ErrorPage = ErrorPage
	var rec perf.Recorder
	opts.Wrap = perf.Middleware(&rec, perf.Options{Started: started, Logger: opts.Logger})
}
