package myapp

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/csrf"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/migrate"
	"amadan.net/rastrillo/rastrillo/password"
	"amadan.net/rastrillo/rastrillo/perf"
	"amadan.net/rastrillo/rastrillo/sessions"

	"myproject/libs/changes"
	"myproject/libs/doorbell"
	"myproject/libs/telemetry"
)

// App wires the whole app: schema, router, static files. It returns a
// *http.ServeMux because rastrillo.Options.Mux is typed that way — the
// chi router mounts inside it. Router and Mux are its two halves,
// separate only so the perf test can walk the chi routes while
// measuring through the same handler production serves. origin is the
// app's external origin, the CSRF check's yardstick and the cookie
// attributes' too.
//
// Past 5,000 lines here, split by feature: a package per feature under
// internal/myapp/, each with Mount(r chi.Router, deps) called from
// Router (docs/site/testing.md).
func App(d *db.DB, origin string, logger *slog.Logger) (*http.ServeMux, error) {
	r, err := Router(d, origin, logger)
	if err != nil {
		return nil, err
	}
	return Mux(r), nil
}

// Router applies the schema, installs the change-record and telemetry
// callbacks and builds the routes.
func Router(d *db.DB, origin string, logger *slog.Logger) (chi.Router, error) {
	migrated, err := migrate.Apply(context.Background(), d, BootSchema)
	if err != nil {
		return nil, err
	}
	logger.Info("migrate", "applied", migrated.Applied,
		"skipped", migrated.Skipped, "adopted", migrated.Adopted)

	// Committed rings the doorbell (libs/doorbell, ADR 0004): after a
	// commit landed changes within a note, never for a rollback. The
	// bell closes when the process drains, so Serve's shutdown is not
	// held open by streams.
	bell := doorbell.NewBell(maxStreams)
	go func() { <-doorbell.Draining(); bell.Close() }()

	// Every write on a tracked table must be explained (libs/changes),
	// by a reason that declares the table and columns it writes
	// (ReasonWrites). Installed on the one *gorm.DB every handler
	// writes through, so there is no unhooked path.
	// Install also takes the boot census: a table in neither Tracked
	// nor Untracked stops the app here, named.
	// Invariants are the rules a write must name (By) and a refusal
	// may (Refuse); a Trail opened by policy.Begin logs its refusals
	// on the app's logger.
	policy := changes.Policy{Tracked: Tracked, Untracked: Untracked, Exempt: TrackedExempt, Correlation: Correlation,
		Allow: ReasonWrites, Invariants: Invariants, Logger: logger, Committed: ringNotes(bell)}
	if err := changes.Install(d.G, policy); err != nil {
		return nil, err
	}
	// Every statement on the same handle gets a span and a duration
	// sample: the request's child when the query was given r.Context().
	if err := telemetry.DB(d.G); err != nil {
		return nil, err
	}

	writer, err := d.G.DB()
	if err != nil {
		return nil, err
	}
	sess, err := sessions.New(sessions.Config{DB: writer, Origin: origin, Logger: logger})
	if err != nil {
		return nil, err
	}
	a := &app{db: d, logger: logger, sess: sess, bell: bell, policy: policy}

	ph, err := password.New(password.Config{
		Sessions:     sess,
		ProofOff:     true, // one tenant's instance, invited members only; no public front door yet
		Lookup:       a.lookupMember,
		Create:       a.createMember,
		RenderSignin: a.renderSignin,
		RenderSignup: a.renderSignup,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	// Telemetry first, so the span covers every middleware below it,
	// CSRF and sessions included, on every route at once.
	r.Use(telemetry.HTTP)
	r.Use(correlation)

	// Browsers: same-origin evidence on every POST, a session cookie.
	r.Group(func(r chi.Router) {
		r.Use(csrf.Protect(origin))
		r.Use(sess.Middleware)

		r.Get("/signin", ph.SigninPage)
		r.Post("/signin", ph.Signin)
		r.Get("/signup", ph.SignupPage)
		r.Post("/signup", withSignupName(ph.Signup))
		r.Post("/signout", ph.Signout)

		r.Group(func(r chi.Router) {
			r.Use(sess.Require)
			r.Get("/", a.notes)
			r.Get("/notes/events", a.notesEvents)
			r.Get("/notes/new", a.noteNew)
			r.Post("/notes", a.noteCreate)
			r.Get("/notes/{id}/correct", a.noteCorrectNew)
			r.Post("/notes/{id}/correct", a.noteCorrect)
			r.Get("/changes", a.changeLog)
			r.Get("/changes.json", a.changeFeed)
		})
	})

	// Agents (a webhook, a schedule tick) go in a group of their own
	// here: bearer-authenticated POSTs under /agent/, with no browser
	// evidence to demand and no session. Each opens its own Trail with
	// actor agent:<name>.
	return r, nil
}

// maxStreams caps the doorbell streams one process holds open: one per
// open notes page. Past it a page still works, and says to reload.
const maxStreams = 256

// ringNotes is the doorbell's ring: a change within a note ("note:12")
// rings that note's listeners and the notes list's (NotesList), which
// shows every note.
func ringNotes(bell *doorbell.Bell) func(scopes []string) {
	return func(scopes []string) {
		for _, s := range scopes {
			if strings.HasPrefix(s, "note:") {
				scopes = append(scopes[:len(scopes):len(scopes)], NotesList) // a copy: the slice is libs/changes'
				break
			}
		}
		bell.Ring(scopes)
	}
}

// Mux mounts the static files beside the router.
func Mux(r http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", assets.Handler())
	if devReload {
		mux.HandleFunc("GET /dev/boot", devBoot)
	}
	mux.Handle("/", r)
	return mux
}

// Configure is the one place the serving options are set, so main.go
// and the perf test cannot drift apart.
func Configure(opts *rastrillo.Options, mux *http.ServeMux, started time.Time) {
	opts.Mux = mux
	opts.ErrorPage = ErrorPage
	var rec perf.Recorder
	opts.Wrap = perf.Middleware(&rec, perf.Options{Started: started, Logger: opts.Logger})
}

// correlation sends the request's correlation back out as
// X-Correlation-Id, so a person can quote it. The id is the request's
// trace id, which telemetry.HTTP has already put in the context: the
// caller's when a traceparent came in. Nothing hands it on by hand
// from here: every log line, span and change record reads it off the
// context it runs in.
func correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Correlation-Id", Correlation(r.Context()))
		next.ServeHTTP(w, r)
	})
}

// Correlation returns the correlation id of the request ctx belongs
// to: its trace id.
func Correlation(ctx context.Context) string { return telemetry.TraceID(ctx) }
