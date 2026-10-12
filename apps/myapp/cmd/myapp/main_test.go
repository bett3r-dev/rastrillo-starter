package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/budget"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

// Scale to zero (ADR 0005): with MYAPP_IDLE_TIMEOUT set, the process
// sends itself the SIGTERM systemd would once nothing has been in
// flight for the timeout, and never while a request (an open doorbell
// stream, say) is still being served. One test, not two in parallel:
// both would be waiting on the same process's signal.
func TestTheProcessStopsItselfWhenIdleAndNotBefore(t *testing.T) {
	t.Parallel()
	const timeout = 50 * time.Millisecond
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	defer signal.Stop(sig)

	var opts rastrillo.Options
	if err := exitWhenIdle(&opts, timeout.String(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	h := opts.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	<-started

	select {
	case <-sig:
		t.Fatal("SIGTERM while a request was in flight")
	case <-time.After(5 * timeout):
	}
	close(release)
	select {
	case <-sig:
	case <-time.After(20 * timeout):
		t.Fatal("no SIGTERM after the timeout with nothing in flight")
	}
}

// Unset, the process serves until stopped, as in development; a value
// that is not a positive duration refuses to boot rather than never
// stopping, or stopping at once.
func TestTheIdleTimeoutIsOffUnsetAndRefusedMalformed(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var opts rastrillo.Options
	if err := exitWhenIdle(&opts, "", log); err != nil || opts.Wrap != nil {
		t.Fatalf("unset: err %v, wrapped %v", err, opts.Wrap != nil)
	}
	for _, v := range []string{"soon", "0s", "-1m"} {
		if err := exitWhenIdle(&opts, v, log); err == nil {
			t.Errorf("MYAPP_IDLE_TIMEOUT=%q accepted", v)
		}
	}
}
