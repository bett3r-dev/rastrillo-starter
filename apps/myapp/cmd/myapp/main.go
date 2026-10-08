// Command myapp wires the app: resolve the platform's activation
// argv, open the database, build the app, serve.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/db"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
	"myproject/libs/telemetry"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		os.Exit(verify(os.Args[2:]))
	}
	// First, so perf's cold budget counts the whole start: a hibernated
	// instance pays all of it on someone's click.
	started := time.Now()
	stderr := slog.NewTextHandler(os.Stderr, nil)

	// Telemetry is off unless OTEL_EXPORTER_OTLP_ENDPOINT is set; on,
	// every log line also goes to Loki beside stderr, with the trace it
	// was logged under. MYAPP_TENANT names the tenant this instance
	// serves in every series, span and log stream.
	tel, err := telemetry.Setup(context.Background(), telemetry.Config{
		Service: "myapp",
		Version: rastrillo.BuildVersion,
		Tenant:  tenant(),
	})
	if err != nil {
		slog.New(stderr).Error("telemetry", "err", err)
		os.Exit(1)
	}
	// stderr gets correlation=<trace id> on every line logged in a
	// request; Loki gets the same id as the record's trace_id.
	logger := slog.New(telemetry.Tee(telemetry.Correlate(stderr), tel.Logs))
	// Exit through here once telemetry is up, so a failed boot's log
	// lines are flushed too rather than lost with the process.
	exit := func(code int) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(ctx); err != nil {
			slog.New(stderr).Error("telemetry flush", "err", err)
		}
		os.Exit(code)
	}

	// Resolve, not Run: this app opens its own database handle via
	// db.Open — a *gorm.DB with a split reader/writer pool, not the
	// bare *sql.DB Options.Router would hand back — so Options.DBPath
	// must be empty again by the Serve call below, or Serve would open
	// a second, unused connection to the same file.
	opts, err := rastrillo.Resolve(rastrillo.Options{DBPath: "myapp.db", Logger: logger})
	if err != nil {
		logger.Error("resolve activation", "err", err)
		exit(1)
	}

	d, err := db.Open(opts.DBPath, logger)
	if err != nil {
		logger.Error("open database", "err", err)
		exit(1)
	}
	defer d.Close()
	if err := telemetry.SQLiteFiles(opts.DBPath); err != nil {
		logger.Error("telemetry", "err", err)
		exit(1)
	}

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
		exit(1)
	}

	// Configure sets Mux, ErrorPage (wire render.go's ErrorPage to
	// Ctx.ErrorPage too as the app grows handlers) and perf's timing,
	// the same options the perf test measures through.
	myapp.Configure(&opts, mux, started)
	opts.DBPath = ""
	if err := rastrillo.Serve(opts); err != nil {
		logger.Error("serve failed", "err", err)
		exit(1)
	}
	// Serve returned: SIGTERM, most often the platform hibernating the
	// instance. Push what is still buffered before the process goes.
	// Serve's drain took up to 10s of the activator's 20s; 5s of the
	// rest is ours. d.Close runs after, as a deferred call.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil {
		slog.New(stderr).Error("telemetry flush", "err", err)
	}
}

// tenant is who this instance serves: MYAPP_TENANT, else
// the host's name, so two local runs never pass for one.
func tenant() string {
	if t := os.Getenv("MYAPP_TENANT"); t != "" {
		return t
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// verify is `myapp verify -db <path>`: fold every tracked row's
// change records and compare with the row as it is, on a copy of a
// production database. Prints each disagreement and exits 1 if there
// is one. The check after the fact for a write that met neither the
// hook nor the guard trigger.
func verify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	path := fs.String("db", "", "path to a copy of the database")
	if err := fs.Parse(args); err != nil || *path == "" {
		fmt.Fprintln(os.Stderr, "usage: myapp verify -db <path>")
		return 2
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d, err := db.Open(*path, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer d.Close()
	ms, err := changes.Verify(d.G, myapp.Tracked, myapp.TrackedExempt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, m := range ms {
		fmt.Println(m)
	}
	if len(ms) > 0 {
		fmt.Fprintf(os.Stderr, "%d rows the change record does not explain\n", len(ms))
		return 1
	}
	fmt.Println("the change record explains every tracked row")
	return 0
}
