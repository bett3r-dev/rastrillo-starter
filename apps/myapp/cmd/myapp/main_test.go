package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo"
	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"

	myapp "myapp/internal/myapp"
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

// `myapp verify -db <path>` over an instance where Ana joined: the
// record explains every row, the welcome note's rule included (it is
// a declared invariant, so verify is handed myapp.Invariants), and
// exits 0. A note rewritten behind the hook's back then exits 1.
func TestVerifyExplainsAnInstanceWhereSomeoneJoined(t *testing.T) {
	t.Parallel()
	const origin = "http://app.test"
	path := filepath.Join(t.TempDir(), "myapp.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := myapp.App(d, origin, log)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"Ana"}, "email": {"ana@example.test"}, "password": {"correct horse battery"}}
	req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("join: status %d\n%s", rec.Code, rec.Body.String())
	}
	d.Close()

	if code := verify([]string{"-db", path}); code != 0 {
		t.Fatalf("verify after a join: exit %d, want 0", code)
	}

	d, err = db.Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.G.Exec("UPDATE notes SET title = 'Rewritten'").Error; err != nil {
		t.Fatal(err)
	}
	d.Close()
	if code := verify([]string{"-db", path}); code != 1 {
		t.Fatalf("verify after a write the record does not explain: exit %d, want 1", code)
	}
}
