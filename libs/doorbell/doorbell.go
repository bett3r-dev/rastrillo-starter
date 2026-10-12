// Package doorbell is server push as a doorbell (ADR 0004): "something
// changed, here is the latest change id", never a payload. A page
// listens on its scope's stream and re-fetches the fragments it already
// knows how to render (live.js). Writes stay POSTs.
//
// The Bell is in-process on purpose. One instance is one tenant, served
// by one process (ADR 0005), and SQLite has one writer: every write that
// can ring is made in this process, so every browser that can hear it is
// connected to this process. Fan-out across processes (a broker,
// LISTEN/NOTIFY) would serve a deployment that does not exist here. If
// one ever does, the tick ADR 0004 names (each stream re-reading
// max(change id)) works without one.
//
// The ring source is libs/changes' Policy.Committed, wired to Ring:
// told after a commit landed changes in a scope, never inside the
// transaction, never for a rollback. It sees every tracked write
// through the app's handle, whichever handler or agent made it.
//
// It is blueprint2's kit/bell.go, the generic half: the attach and
// summon lines (blueprint2's StreamAttached, its ADR 0007) stay that
// app's.
package doorbell

import (
	_ "embed"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ErrBellFull is a Listen past the bell's cap: the process holds no
// more open streams. ErrBellClosed is a Listen on a closed bell (the
// process is draining).
var (
	ErrBellFull   = errors.New("doorbell: too many streams open")
	ErrBellClosed = errors.New("doorbell: the bell is closed")
)

// Bell fans a ring out to the listeners of the scopes it names.
type Bell struct {
	mu     sync.Mutex
	max    int
	n      int
	subs   map[string]map[*Listener]struct{}
	closed bool
}

// NewBell is a bell holding at most max listeners at once.
func NewBell(max int) *Bell {
	return &Bell{max: max, subs: map[string]map[*Listener]struct{}{}}
}

// Listener is one stream's place on the bell. C carries a ring (at
// most one waiting: a ring says only that something changed, so two
// unread rings are one); Done closes when the bell does.
type Listener struct {
	C     chan struct{}
	done  chan struct{}
	b     *Bell
	scope string
	once  sync.Once
}

// Done closes when the bell closes: the stream must end.
func (l *Listener) Done() <-chan struct{} { return l.done }

// Listen takes a place for a stream of scope.
func (b *Bell) Listen(scope string) (*Listener, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBellClosed
	}
	if b.n >= b.max {
		return nil, ErrBellFull
	}
	l := &Listener{C: make(chan struct{}, 1), done: make(chan struct{}), b: b, scope: scope}
	if b.subs[scope] == nil {
		b.subs[scope] = map[*Listener]struct{}{}
	}
	b.subs[scope][l] = struct{}{}
	b.n++
	return l, nil
}

// Close gives the listener's place back. Twice is harmless.
func (l *Listener) Close() {
	l.once.Do(func() {
		b := l.b
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[l.scope][l]; !ok {
			return
		}
		delete(b.subs[l.scope], l)
		if len(b.subs[l.scope]) == 0 {
			delete(b.subs, l.scope)
		}
		b.n--
	})
}

// Ring rings every listener of each scope, without blocking: it runs
// on the writer's goroutine, right after the commit.
func (b *Bell) Ring(scopes []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range scopes {
		for l := range b.subs[s] {
			select {
			case l.C <- struct{}{}:
			default:
			}
		}
	}
}

// Listening is how many streams hold a place now.
func (b *Bell) Listening() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

// Close ends every stream and takes no more: a process draining
// (SIGTERM) closes it so its graceful shutdown is not held open by
// streams that would otherwise run out their time to live.
func (b *Bell) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, ls := range b.subs {
		for l := range ls {
			close(l.done)
		}
	}
}

// Draining closes when the process is told to stop, the same SIGTERM
// (or SIGINT) rastrillo.Serve drains on: every channel registered for
// a signal gets it. An app closes its Bell then, so Serve's graceful
// shutdown is not held open for its whole 10 s by streams it does not
// cancel (Shutdown waits on a request in flight). One channel for the
// process, however many apps a test binary builds.
var Draining = sync.OnceValue(func() <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() { <-sig; close(done) }()
	return done
})

// streamTiming is ADR 0004's operating rules.
type streamTiming struct {
	// heartbeat is the ": ping" on a quiet stream, so a proxy does not
	// time it out and a dead client shows at the next write.
	heartbeat time.Duration
	// ttl ends a stream on its own; EventSource reconnects, sending
	// Last-Event-ID, and is caught up at once.
	ttl time.Duration
	// writeDeadline is re-armed before every write (serve.go # Timeouts:
	// rastrillo's server has no WriteTimeout, so a stream bounds its own).
	writeDeadline time.Duration
}

var defaultTiming = streamTiming{heartbeat: 15 * time.Second, ttl: 5 * time.Minute, writeDeadline: 30 * time.Second}

// retryAfter is what a full bell asks a client to wait, in seconds.
const retryAfter = "30"

// Stream serves scope's doorbell on b: an "update" event, id and data
// the scope's head (head(), the latest change id in it), each time a
// ring finds the head past what the client has. What the client has is
// its Last-Event-ID (EventSource sends it on every reconnect), else
// ?after= (the head the page was rendered at, for the first
// connection), else the head now. A cursor behind the head is rung at
// once. The handler gates nothing: the route's own middleware decides
// who may listen. A full bell is a 503 with Retry-After, before any
// stream.
func Stream(w http.ResponseWriter, r *http.Request, b *Bell, scope string, head func() int64) {
	stream(w, r, b, scope, head, defaultTiming)
}

func stream(w http.ResponseWriter, r *http.Request, b *Bell, scope string, head func() int64, tm streamTiming) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Listen before reading the head: a commit between the two then
	// rings this stream rather than falling in the gap.
	l, err := b.Listen(scope)
	if err != nil {
		w.Header().Set("Retry-After", retryAfter)
		http.Error(w, "Too many live pages are open; this page still works, reload it to see changes.", http.StatusServiceUnavailable)
		return
	}
	defer l.Close()
	last, has := cursor(r)
	if !has {
		last = head()
	}

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	// write is every byte's one exit: the deadline armed (a writer that
	// cannot set one, a test recorder, still streams), the bytes, the
	// flush. A write error ends the stream.
	write := func(text string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(tm.writeDeadline))
		if _, err := io.WriteString(w, text); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	// ring sends the head when it moved past the client's.
	ring := func() bool {
		h := head()
		if h <= last {
			return true
		}
		last = h
		id := strconv.FormatInt(h, 10)
		return write("id: " + id + "\nevent: update\ndata: " + id + "\n\n")
	}
	if !write("retry: 3000\n\n") || !ring() {
		return
	}

	ttl := time.NewTimer(tm.ttl)
	defer ttl.Stop()
	beat := time.NewTicker(tm.heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-l.Done():
			return
		case <-ttl.C:
			return
		case <-beat.C:
			if !write(": ping\n\n") {
				return
			}
		case <-l.C:
			if !ring() {
				return
			}
		}
	}
}

// cursor is the change id the client has: Last-Event-ID, else ?after=.
// One that is not a number counts as 0, so the client is caught up.
func cursor(r *http.Request) (int64, bool) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, true
	}
	return n, true
}

// LiveJS is live.js, the page's half of the doorbell. An app vendors it
// as static/live.js (its own assets are embedded in its own module) and
// a test pins the copy to this one, so the two cannot drift.
//
//go:embed live.js
var LiveJS []byte
