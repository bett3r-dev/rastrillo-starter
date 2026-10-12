package doorbell

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo/budget"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

// A ring reaches the listeners of the scopes it names, and only them;
// a ring nobody takes up yet is kept (one, not a queue), never lost.
func TestBellRingsTheScopesListeners(t *testing.T) {
	t.Parallel()
	b := NewBell(4)
	one, err := b.Listen("session:1")
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, _ := b.Listen("session:2")
	defer two.Close()

	b.Ring([]string{"session:1"})
	b.Ring([]string{"session:1"}) // a second ring folds into the first
	select {
	case <-one.C:
	default:
		t.Fatal("session:1's listener was not rung")
	}
	select {
	case <-one.C:
		t.Fatal("two rings queued: a ring says only that something changed")
	case <-two.C:
		t.Fatal("session:2's listener was rung for session:1")
	default:
	}
}

// The bell holds a fixed number of listeners per process; a closed
// listener frees its place, and closing the bell ends every stream.
func TestBellIsCappedAndCloses(t *testing.T) {
	t.Parallel()
	b := NewBell(2)
	l1, _ := b.Listen("session:1")
	l2, _ := b.Listen("session:1")
	if _, err := b.Listen("session:1"); !errors.Is(err, ErrBellFull) {
		t.Fatalf("third listener: err = %v, want ErrBellFull", err)
	}
	l1.Close()
	l1.Close() // twice is harmless
	l3, err := b.Listen("session:2")
	if err != nil {
		t.Fatalf("a closed listener did not free its place: %v", err)
	}
	b.Close()
	for _, l := range []*Listener{l2, l3} {
		select {
		case <-l.Done():
		default:
			t.Fatal("closing the bell left a listener open")
		}
	}
	if _, err := b.Listen("session:1"); !errors.Is(err, ErrBellClosed) {
		t.Fatalf("listening on a closed bell: err = %v, want ErrBellClosed", err)
	}
}

// event is one server-sent event as a client reads it.
type event struct{ id, name, data string }

// read is the stream's events (comments skipped) on a channel, closed
// when the stream ends.
func read(t *testing.T, body *bufio.Reader) (<-chan event, <-chan string) {
	t.Helper()
	out, comments := make(chan event, 16), make(chan string, 16)
	go func() {
		defer close(out)
		var e event
		for {
			line, err := body.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				if e.name != "" {
					out <- e
				}
				e = event{}
			case strings.HasPrefix(line, ":"):
				comments <- line
			case strings.HasPrefix(line, "id: "):
				e.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out, comments
}

// open serves one stream of scope over a real connection, with head
// as the table's latest change id and the timing given.
func open(t *testing.T, b *Bell, head *atomic.Int64, tm streamTiming, header map[string]string, query string) (*http.Response, <-chan event, <-chan string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, b, "session:1", func() int64 { return head.Load() }, tm)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events"+query, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	evs, comments := read(t, bufio.NewReader(res.Body))
	return res, evs, comments
}

func next(t *testing.T, evs <-chan event) event {
	t.Helper()
	select {
	case e, ok := <-evs:
		if !ok {
			t.Fatal("the stream ended")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return event{}
}

func quiet(t *testing.T, evs <-chan event) {
	t.Helper()
	select {
	case e := <-evs:
		t.Fatalf("an event, want none: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

var fast = streamTiming{heartbeat: time.Hour, ttl: time.Hour, writeDeadline: time.Second}

// A stream answers text/event-stream; a ring whose head moved past
// what the client has sends "update" with the head as id and data, a
// ring that moved nothing sends nothing.
func TestStreamSendsTheHeadWhenItMoves(t *testing.T) {
	t.Parallel()
	b := NewBell(4)
	var head atomic.Int64
	head.Store(7)
	res, evs, _ := open(t, b, &head, fast, nil, "")
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Content-Type %q, Cache-Control %q", ct, res.Header.Get("Cache-Control"))
	}
	waitListening(t, b, 1)
	quiet(t, evs) // no cursor: the page is as fresh as the stream
	b.Ring([]string{"session:1"})
	quiet(t, evs) // rung, but nothing moved (a rolled-back savepoint, say)
	head.Store(9)
	b.Ring([]string{"session:1"})
	if e := next(t, evs); e != (event{id: "9", name: "update", data: "9"}) {
		t.Fatalf("event %+v, want update 9", e)
	}
}

// Reconnecting with Last-Event-ID (or the page's ?after= on its first
// connection) behind the head is rung at once; at the head, nothing.
// Last-Event-ID wins over after.
func TestStreamCatchesUpACursorBehindTheHead(t *testing.T) {
	t.Parallel()
	var head atomic.Int64
	head.Store(12)
	_, evs, _ := open(t, NewBell(4), &head, fast, map[string]string{"Last-Event-ID": "10"}, "?after=12")
	if e := next(t, evs); e.id != "12" || e.name != "update" {
		t.Fatalf("event %+v, want update 12", e)
	}
	_, evs, _ = open(t, NewBell(4), &head, fast, nil, "?after=11")
	if e := next(t, evs); e.id != "12" {
		t.Fatalf("event %+v, want update 12", e)
	}
	_, evs, _ = open(t, NewBell(4), &head, fast, map[string]string{"Last-Event-ID": "12"}, "?after=3")
	quiet(t, evs)
}

// A quiet stream sends a comment heartbeat, and ends itself after its
// time to live (EventSource reconnects with Last-Event-ID).
func TestStreamHeartbeatsAndEnds(t *testing.T) {
	t.Parallel()
	var head atomic.Int64
	_, evs, comments := open(t, NewBell(4), &head,
		streamTiming{heartbeat: 20 * time.Millisecond, ttl: 200 * time.Millisecond, writeDeadline: time.Second}, nil, "")
	select {
	case c := <-comments:
		if c != ": ping" {
			t.Fatalf("comment %q, want : ping", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat")
	}
	select {
	case _, ok := <-evs:
		for ok {
			_, ok = <-evs
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its time to live")
	}
}

// A full bell answers 503 with Retry-After, not a stream; a closed
// bell (the process draining) ends the streams open on it.
func TestStreamRefusedWhenFullAndEndedOnClose(t *testing.T) {
	t.Parallel()
	b := NewBell(1)
	var head atomic.Int64
	_, evs, _ := open(t, b, &head, fast, nil, "")
	waitListening(t, b, 1)
	res, _, _ := open(t, b, &head, fast, nil, "")
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" {
		t.Fatalf("full bell: %d, Retry-After %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	b.Close()
	select {
	case _, ok := <-evs:
		for ok {
			_, ok = <-evs
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the bell left the stream open")
	}
}

// A client gone frees its place.
func TestStreamCleansUpWhenTheClientGoes(t *testing.T) {
	t.Parallel()
	b := NewBell(4)
	var head atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, b, "session:1", func() int64 { return head.Load() }, fast)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	waitListening(t, b, 1)
	cancel()
	res.Body.Close()
	waitListening(t, b, 0)
}

func waitListening(t *testing.T, b *Bell, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.Listening() != n {
		if time.Now().After(deadline) {
			t.Fatalf("listening %d, want %d", b.Listening(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
