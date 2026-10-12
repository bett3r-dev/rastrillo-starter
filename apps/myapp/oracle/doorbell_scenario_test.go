//go:build oracle

package oracle_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The notes list's doorbell (ADR 0004) over a real connection: an
// event only says something changed, and carries the notes' head.

type sse struct{ id, name, data string }

type stream struct {
	t      *testing.T
	res    *http.Response
	events chan sse
}

// watchesNotes is p's browser on the notes page opening its stream, as
// the page's script does; header adds what a reconnect sends.
func (p *person) watchesNotes(query string, header http.Header) *stream {
	p.s.t.Helper()
	t := p.s.t
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.s.base+"/notes/events"+query, nil)
	for k, vs := range header {
		req.Header[k] = vs
	}
	// The person's cookies, on a client with no timeout: a stream
	// stays open for as long as the page does.
	client := &http.Client{Jar: p.client.Jar, CheckRedirect: p.client.CheckRedirect}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	st := &stream{t: t, res: res, events: make(chan sse, 16)}
	if res.Header.Get("Content-Type") != "text/event-stream" {
		close(st.events)
		return st
	}
	go func() {
		defer close(st.events)
		br := bufio.NewReader(res.Body)
		var e sse
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "" && e.name != "":
				st.events <- e
				e = sse{}
			case strings.HasPrefix(line, "id: "):
				e.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return st
}

// isOpen asserts the stream answered as a stream.
func (st *stream) isOpen() *stream {
	st.t.Helper()
	if st.res.StatusCode != http.StatusOK || st.res.Header.Get("Content-Type") != "text/event-stream" {
		st.t.Fatalf("events: %d %q, want 200 text/event-stream", st.res.StatusCode, st.res.Header.Get("Content-Type"))
	}
	return st
}

// rings asserts the next event is update with head as its id and data.
func (st *stream) rings(head int64) {
	st.t.Helper()
	select {
	case e, ok := <-st.events:
		if !ok {
			st.t.Fatalf("the stream ended, want update %d", head)
		}
		if want := (sse{id: itoa(head), name: "update", data: itoa(head)}); e != want {
			st.t.Fatalf("event %+v, want %+v", e, want)
		}
	case <-time.After(5 * time.Second):
		st.t.Fatalf("no ring, want update %d", head)
	}
}

// staysQuiet asserts no event comes for a while.
func (st *stream) staysQuiet() {
	st.t.Helper()
	select {
	case e, ok := <-st.events:
		if ok {
			st.t.Fatalf("event %+v, want none", e)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// Given Ana and Luis with the notes page open, When Ana writes a note,
// Then both pages ring once with the notes' new head, the note's
// change.
func TestScenarioWritingANoteRingsEveryNotesPage(t *testing.T) {
	t.Parallel()
	s := given(t)
	people := s.members("Ana", "Luis")
	ana, luis := people[0], people[1]
	luissPage := luis.watchesNotes("", nil).isOpen()
	anasPage := ana.watchesNotes("", nil).isOpen()
	s.mark_()

	redirected(t, ana.writesNote("Groceries", "Milk"), "/")

	written := s.changed(ReasonNoteWritten)[0]
	luissPage.rings(written.ID)
	anasPage.rings(written.ID)
	luissPage.staysQuiet()
}

// Given two notes written while Luis's stream was down, When it
// reconnects with the Last-Event-ID it last had, Then it is rung at
// once with the head; reconnecting at the head is quiet.
func TestScenarioAReconnectCatchesUp(t *testing.T) {
	t.Parallel()
	s := given(t)
	people := s.members("Ana", "Luis")
	ana, luis := people[0], people[1]
	redirected(t, ana.writesNote("Groceries", "Milk"), "/")
	seen := s.notesHead()
	redirected(t, ana.writesNote("Chores", "Bins"), "/")
	redirected(t, ana.writesNote("Calls", "Mum"), "/")
	head := s.notesHead()

	luis.watchesNotes("", http.Header{"Last-Event-Id": {itoa(seen)}}).isOpen().rings(head)
	luis.watchesNotes("", http.Header{"Last-Event-Id": {itoa(head)}}).isOpen().staysQuiet()
	luis.watchesNotes("?after="+itoa(seen), nil).isOpen().rings(head)
	luis.watchesNotes("?after="+itoa(head), nil).isOpen().staysQuiet()
}

// A signed-out browser is sent to sign in, not streamed to.
func TestScenarioOnlyMembersListen(t *testing.T) {
	t.Parallel()
	s := given(t)
	out := s.visitor().watchesNotes("", nil)
	if out.res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(out.res.Header.Get("Location"), "/signin") {
		t.Fatalf("signed out: %d to %q, want the way to /signin", out.res.StatusCode, out.res.Header.Get("Location"))
	}
}

// The notes page wires itself to the doorbell: the stream and the head
// it was rendered at, and the route that refreshes the list, which
// asked as a fragment is the list alone.
func TestScenarioTheNotesPageSaysWhatRefreshesIt(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceries", "Milk"), "/")

	ana.sees("/", `data-events="/notes/events"`, `data-head="`+itoa(s.notesHead())+`"`, `data-live="/"`)
	req := s.request(http.MethodGet, "/", nil)
	req.Header.Set("Rastrillo-Fragment", "1")
	rec := ana.do(req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body, "Groceries") || strings.Contains(rec.Body, "<html") {
		t.Fatalf("the list as a fragment: %d\n%s", rec.Code, rec.Body)
	}
}
