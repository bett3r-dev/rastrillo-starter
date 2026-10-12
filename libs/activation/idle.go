package activation

import (
	"net/http"
	"sync"
	"time"
)

// Idle tells a process when it has served nothing for a while. Its
// Middleware counts requests in flight; Done closes once the count has
// stayed at zero for the whole timeout. An SSE stream is a request
// that has not finished, so an open one keeps the process awake with
// nothing special for it.
//
// The clock starts at NewIdle, so a process started by a connection
// that never sends a request still exits. Done closes once and stays
// closed: what to do then (a graceful shutdown, which still serves the
// requests that slip in meanwhile) is the caller's.
type Idle struct {
	timeout time.Duration
	after   func(time.Duration, func()) stopper

	mu       sync.Mutex
	inFlight int
	gen      uint64 // bumped on every 0→1, so a stale timer knows it is stale
	timer    stopper
	done     chan struct{}
	closed   bool
}

type stopper interface{ Stop() bool }

// NewIdle starts the idle clock: with no request, Done closes after
// timeout.
func NewIdle(timeout time.Duration) *Idle {
	return newIdle(timeout, func(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) })
}

func newIdle(timeout time.Duration, after func(time.Duration, func()) stopper) *Idle {
	i := &Idle{timeout: timeout, after: after, done: make(chan struct{})}
	i.mu.Lock()
	i.arm()
	i.mu.Unlock()
	return i
}

// Done is closed once the process has had no request in flight for
// the timeout.
func (i *Idle) Done() <-chan struct{} { return i.done }

// InFlight is the number of requests being served now.
func (i *Idle) InFlight() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.inFlight
}

// Middleware counts each request from the moment it reaches next until
// next returns, a panic included.
func (i *Idle) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i.begin()
		defer i.end()
		next.ServeHTTP(w, r)
	})
}

func (i *Idle) begin() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inFlight++
	if i.inFlight == 1 {
		i.gen++
		if i.timer != nil {
			i.timer.Stop()
			i.timer = nil
		}
	}
}

func (i *Idle) end() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inFlight--
	if i.inFlight == 0 {
		i.arm()
	}
}

// arm starts the timeout for the current idle stretch. Called with mu
// held. A timer that fires after a request began finds gen moved on
// and does nothing, which is what Stop alone cannot promise.
func (i *Idle) arm() {
	gen := i.gen
	i.timer = i.after(i.timeout, func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		if i.gen != gen || i.inFlight != 0 || i.closed {
			return
		}
		i.closed = true
		close(i.done)
	})
}
