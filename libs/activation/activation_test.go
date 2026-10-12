package activation

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo/budget"
)

// childEnv marks the re-executed test binary as the socket-activated
// process. See TestListenersTakesFD3FromTheParent.
const childEnv = "ACTIVATION_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(child())
	}
	os.Exit(budget.Main(m))
}

// child is the activated process. systemd sets LISTEN_PID between fork
// and exec, when the child's pid is known; os/exec has no hook there,
// so the parent cannot. The child sets it to its own pid as its first
// act, standing in for that one step, and everything after it is the
// real path: fd 3 inherited across exec, the pid check, FDNAMES,
// net.FileListener, the variables unset.
func child() int {
	os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	ls, err := Named()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	l := ls["web"]
	if len(ls) != 1 || l == nil {
		fmt.Fprintf(os.Stderr, "want one listener named web, got %v\n", ls)
		return 1
	}
	for _, k := range []string{"LISTEN_FDS", "LISTEN_PID", "LISTEN_FDNAMES"} {
		if v, ok := os.LookupEnv(k); ok {
			fmt.Fprintf(os.Stderr, "%s still set: %q\n", k, v)
			return 1
		}
	}
	c, err := l.Accept()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(c, "served by %d\n", os.Getpid())
	c.Close()
	return 0
}

func TestListenersTakesFD3FromTheParent(t *testing.T) {
	// Not t.TempDir: its name makes the path longer than a unix
	// socket's 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "act")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", dir+"/a.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	f, err := l.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.ExtraFiles = []*os.File{f} // ExtraFiles[0] is fd 3 in the child
	cmd.Env = append(os.Environ(), childEnv+"=1", "LISTEN_FDS=1", "LISTEN_FDNAMES=web")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The parent only holds the socket, as systemd does: the dial waits
	// in its backlog until the child accepts.
	c, err := net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	// A child that fails never accepts: drop the connection when it
	// exits rather than waiting out the deadline.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait(); c.Close() }()
	line, err := bufio.NewReader(c).ReadString('\n')
	if werr := <-waited; werr != nil {
		t.Fatalf("child: %v\n%s", werr, stderr.String())
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("served by %d\n", cmd.Process.Pid); line != want {
		t.Fatalf("got %q, want %q", line, want)
	}
}

func env(vars map[string]string) (func(string) string, func(string) error) {
	return func(k string) string { return vars[k] },
		func(k string) error { delete(vars, k); return nil }
}

func TestListenersRefusesWhatIsNotItsOwn(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"not activated":     {},
		"another pid":       {"LISTEN_FDS": "1", "LISTEN_PID": "1"},
		"no pid":            {"LISTEN_FDS": "1"},
		"zero fds":          {"LISTEN_FDS": "0", "LISTEN_PID": "42"},
		"names, no sockets": {"LISTEN_FDNAMES": "web", "LISTEN_PID": "42"},
	} {
		t.Run(name, func(t *testing.T) {
			getenv, unsetenv := env(vars)
			ls, _, err := listeners(getenv, unsetenv, 42)
			if ls != nil || err != nil {
				t.Fatalf("got %v, %v; want nil, nil", ls, err)
			}
			if len(vars) != 0 {
				t.Fatalf("left %v set", vars)
			}
		})
	}
}

func TestListenersRejectsAGarbledCount(t *testing.T) {
	vars := map[string]string{"LISTEN_FDS": "x", "LISTEN_PID": "42"}
	getenv, unsetenv := env(vars)
	if _, _, err := listeners(getenv, unsetenv, 42); err == nil {
		t.Fatal("want an error")
	}
	if len(vars) != 0 {
		t.Fatalf("left %v set", vars)
	}
}

// fakeClock runs a timer only when the test says the time has come.
type fakeClock struct{ timers []*fakeTimer }

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { s := !t.stopped; t.stopped = true; return s }

func (c *fakeClock) after(d time.Duration, f func()) stopper {
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return t
}

// fire runs every timer armed so far, stopped or not: a real timer can
// fire in the instant before Stop, and Idle must not trust Stop.
func (c *fakeClock) fire() {
	ts := c.timers
	c.timers = nil
	for _, t := range ts {
		t.f()
	}
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestIdleClosesAfterTheTimeoutWithNothingInFlight(t *testing.T) {
	var c fakeClock
	i := newIdle(time.Minute, c.after)
	if closed(i.Done()) {
		t.Fatal("closed before any time passed")
	}
	if len(c.timers) != 1 || c.timers[0].d != time.Minute {
		t.Fatalf("want one timer of a minute, got %+v", c.timers)
	}
	c.fire()
	if !closed(i.Done()) {
		t.Fatal("still open after the timeout")
	}
}

func TestIdleStaysOpenWhileARequestIsInFlight(t *testing.T) {
	var c fakeClock
	i := newIdle(time.Minute, c.after)
	i.begin()
	c.fire() // the boot timer, firing late: the request has begun
	if closed(i.Done()) {
		t.Fatal("closed with a request in flight")
	}
	i.begin()
	i.end()
	if len(c.timers) != 0 {
		t.Fatal("armed with a request still in flight")
	}
	i.end()
	if closed(i.Done()) {
		t.Fatal("closed the moment the last request ended")
	}
	c.fire()
	if !closed(i.Done()) {
		t.Fatal("still open a timeout after the last request")
	}
}

func TestIdleIgnoresATimerFromAnEarlierIdleStretch(t *testing.T) {
	var c fakeClock
	i := newIdle(time.Minute, c.after)
	boot := c.timers[0]
	c.timers = nil
	i.begin()
	i.end() // a new stretch, a new timer
	boot.f()
	if closed(i.Done()) {
		t.Fatal("the boot timer closed a stretch that began after it")
	}
	c.fire()
	if !closed(i.Done()) {
		t.Fatal("the current stretch's timer did not close it")
	}
}

// The real clock, end to end through the middleware: a stream held
// open past the timeout keeps the process awake; once it ends, Done
// closes a timeout later.
func TestIdleMiddlewareHoldsWhileAStreamIsOpen(t *testing.T) {
	const timeout = 30 * time.Millisecond
	i := NewIdle(timeout)
	release := make(chan struct{})
	started := make(chan struct{})
	srv := httptest.NewServer(i.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(started)
		<-release
	})))
	defer srv.Close()

	go func() {
		res, err := http.Get(srv.URL)
		if err == nil {
			res.Body.Close()
		}
	}()
	<-started
	if n := i.InFlight(); n != 1 {
		t.Fatalf("in flight: %d, want 1", n)
	}
	select {
	case <-i.Done():
		t.Fatal("went idle with a stream open")
	case <-time.After(3 * timeout):
	}
	close(release)
	select {
	case <-i.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("never went idle after the stream ended")
	}
}
