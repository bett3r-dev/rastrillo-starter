package myapptest

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The route gate (gastos's coverage_test.go): every route the router
// mounts is reached by a scenario, or is named below with the reason
// none needs to. The change hook, the guard and noBleed fire only on a
// path that runs, so a route nothing calls is a write path no
// convention has looked at (rustrillo's docs/experiments/rust-mutation:
// all six Go mutants that passed the gate were on such a route).
// Coverage is the hook's whole weakness; this makes it a failure
// instead of a silence.
//
// How: newApp walks the chi router once for the mounted (method,
// pattern) pairs, and wraps every app's handler so each request a
// TestScenario* test sends is resolved to its pattern after it is
// served (chi's Find, so /notes/7/correct counts as
// /notes/{id}/correct). TestMain checks the two sets after m.Run, when
// every scenario has finished. A request turned away by sign-in (a 303
// to /signin) does not count: the handler never ran.

// notScenarioRoutes are chi routes no scenario has to reach, route ->
// reason. Empty today: every route, the password plugin's included, is
// reached (screens_scenario_test.go walks the forms and the way in and
// out). The framework's own routes are not chi routes and never get
// here: /static/ is mounted on the ServeMux beside the router (Mux),
// /healthz and /api/version are answered by rastrillo outside it, and
// /dev/boot exists only under MYAPP_DEV_RELOAD. An entry needs a
// reason a reviewer can check, and an entry for a route no longer
// mounted, or one a scenario now reaches, fails the gate.
var notScenarioRoutes = map[string]string{}

// routeCoverage is every mounted route and the scenarios that reached
// each, for the whole test binary (tests run in parallel: hence the
// lock).
var routeCoverage = struct {
	sync.Mutex
	once    sync.Once
	mounted []string
	reached map[string]bool
	runs    int // TestScenario* apps built
}{reached: map[string]bool{}}

// coverRoutes records the router's routes (once) and returns h wrapped
// to record what a scenario reaches.
func coverRoutes(t *testing.T, r chi.Router, h http.Handler) http.Handler {
	routeCoverage.once.Do(func() {
		var mounted []string
		err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			mounted = append(mounted, method+" "+route)
			return nil
		})
		if err != nil {
			t.Fatalf("walk routes: %v", err)
		}
		sort.Strings(mounted)
		routeCoverage.Lock()
		routeCoverage.mounted = mounted
		routeCoverage.Unlock()
	})
	if !strings.HasPrefix(t.Name(), "TestScenario") {
		return h
	}
	routeCoverage.Lock()
	routeCoverage.runs++
	routeCoverage.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, req)
		if sw.code == http.StatusSeeOther && strings.HasPrefix(w.Header().Get("Location"), "/signin") {
			return
		}
		pattern := r.Find(chi.NewRouteContext(), req.Method, req.URL.Path)
		if pattern == "" {
			return
		}
		routeCoverage.Lock()
		routeCoverage.reached[req.Method+" "+pattern] = true
		routeCoverage.Unlock()
	})
}

// statusWriter keeps the first status a handler wrote. It flushes and
// unwraps, so the doorbell's stream works through it.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// routeGate is TestMain's step after m.Run. It returns the exit code.
// A narrowed run (-run or -skip) has not run every scenario, so it
// cannot say a route is unreached, and checks nothing.
func routeGate() int {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return 0
		}
	}
	routeCoverage.Lock()
	defer routeCoverage.Unlock()
	if routeCoverage.runs == 0 || len(routeCoverage.mounted) == 0 {
		fmt.Fprintln(os.Stderr, "routes: no scenario ran, so no route can be said to be reached")
		return 1
	}
	var missing, stale []string
	for _, route := range routeCoverage.mounted {
		if !routeCoverage.reached[route] && notScenarioRoutes[route] == "" {
			missing = append(missing, route)
		}
	}
	for route := range notScenarioRoutes {
		switch {
		case !slices.Contains(routeCoverage.mounted, route):
			stale = append(stale, route+", which is no longer mounted")
		case routeCoverage.reached[route]:
			stale = append(stale, route+", which a scenario now reaches")
		}
	}
	sort.Strings(stale)
	for _, route := range missing {
		fmt.Fprintf(os.Stderr, "routes: no scenario reaches %s; write one, or name it in notScenarioRoutes (coverage_test.go) with a reason\n", route)
	}
	for _, route := range stale {
		fmt.Fprintf(os.Stderr, "routes: notScenarioRoutes names %s; delete the entry\n", route)
	}
	if len(missing)+len(stale) > 0 {
		return 1
	}
	return 0
}
