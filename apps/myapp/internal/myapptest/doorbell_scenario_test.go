package myapptest

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
	"myproject/libs/doorbell"
	"myproject/libs/telemetry"
)

// The doorbell (ADR 0004, libs/doorbell): GET /notes/events is a
// text/event-stream for anyone signed in. When a change lands within
// a note, every notes page open gets "update" with the notes' head
// (the latest change id within any note) as its id and data, and
// nothing else: the page re-fetches its list through the list's own
// route as a fragment (static/live.js). Writes stay POSTs.

// stream is one browser's open events stream.
type stream struct {
	t      *testing.T
	res    *http.Response
	events chan sse
}

type sse struct{ id, name, data string }

// live is the instance over a real connection: a stream cannot be read
// from a ResponseRecorder while its handler runs.
func (s *scenario) live() *httptest.Server {
	s.t.Helper()
	srv := httptest.NewServer(s.h)
	s.t.Cleanup(srv.Close)
	return srv
}

// watchesNotes is p's browser on the notes page opening its stream, as
// the page's script does; header adds what a reconnect sends.
func (p *person) watchesNotes(srv *httptest.Server, query string, header http.Header) *stream {
	p.s.t.Helper()
	return openStream(p.s.t, srv, "/notes/events"+query, header, p.cookie)
}

// openStream is a GET of the events stream at path, read as a stream
// when it answers as one.
func openStream(t *testing.T, srv *httptest.Server, path string, header http.Header, cookie *http.Cookie) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	for k, vs := range header {
		req.Header[k] = vs
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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

// notesHead is the latest change id within any note.
func (s *scenario) notesHead() int64 {
	s.t.Helper()
	var head int64
	s.db.G.Model(&changes.Change{}).Where("scope LIKE 'note:%'").Select("COALESCE(MAX(id), 0)").Scan(&head)
	return head
}

// aRolledBackNote is a note written as WriteNote writes one, that its
// transaction then takes back: what a refusal after the first write,
// or a failed commit, leaves. It never reaches a route, so nothing of
// it is in the flow map; nothing of it lands either.
func (s *scenario) aRolledBackNote(by *person) {
	s.t.Helper()
	ctx, span := telemetry.Start(s.t.Context(), "a write that rolls back")
	defer span.End()
	t := changes.Begin("member:"+itoa(by.id())).WithinCreated("note").Because(myapp.ReasonNoteWritten, "")
	t.EntryPoint = "WriteNote"
	boom := errors.New("refused after the write")
	err := s.db.G.WithContext(t.Context(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&myapp.Note{AuthorID: by.id(), Title: "Taken back"}).Error; err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		s.t.Fatalf("the write did not roll back: %v", err)
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
	srv := s.live()
	luissPage := luis.watchesNotes(srv, "", nil).isOpen()
	anasPage := ana.watchesNotes(srv, "", nil).isOpen()
	s.mark_()

	redirected(t, ana.writesNote("Groceries", "Milk"), "/")

	written := s.changed(myapp.ReasonNoteWritten)[0]
	luissPage.rings(written.ID)
	anasPage.rings(written.ID) // the writer's own other tabs too
	luissPage.staysQuiet()
}

// Given Luis watching the notes, When a note's write rolls back, Then
// his page stays quiet and nothing landed; the next write that commits
// rings him with its own head.
func TestScenarioARolledBackWriteRingsNothing(t *testing.T) {
	t.Parallel()
	s := given(t)
	people := s.members("Ana", "Luis")
	ana, luis := people[0], people[1]
	luissPage := luis.watchesNotes(s.live(), "", nil).isOpen()
	s.mark_()

	s.aRolledBackNote(ana)

	s.changed()
	luissPage.staysQuiet()
	redirected(t, ana.writesNote("Groceries", "Milk"), "/")
	luissPage.rings(s.changed(myapp.ReasonNoteWritten)[0].ID)
}

// Given two notes written while Luis's stream was down, When it
// reconnects with the Last-Event-ID it last had, Then it is rung at
// once with the head; reconnecting at the head is quiet. The page's
// first connection says the head it was rendered at with ?after=.
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
	srv := s.live()

	luis.watchesNotes(srv, "", http.Header{"Last-Event-Id": {itoa(seen)}}).isOpen().rings(head)
	luis.watchesNotes(srv, "", http.Header{"Last-Event-Id": {itoa(head)}}).isOpen().staysQuiet()
	luis.watchesNotes(srv, "?after="+itoa(seen), nil).isOpen().rings(head)
	luis.watchesNotes(srv, "?after="+itoa(head), nil).isOpen().staysQuiet()
}

// A signed-out browser is sent to sign in, not streamed to.
func TestScenarioOnlyMembersListen(t *testing.T) {
	t.Parallel()
	s := given(t)
	out := openStream(t, s.live(), "/notes/events", nil, nil)
	if out.res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(out.res.Header.Get("Location"), "/signin") {
		t.Fatalf("signed out: %d to %q, want the way to /signin", out.res.StatusCode, out.res.Header.Get("Location"))
	}
}

// The notes page wires itself to the doorbell, inert without scripts:
// the stream and the head it was rendered at on the script, and on the
// list the route that refreshes it. That route asked as a fragment is
// the list alone, not the page.
func TestScenarioTheNotesPageSaysWhatRefreshesIt(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceries", "Milk"), "/")

	ana.sees("/", `data-events="/notes/events"`, `data-head="`+itoa(s.notesHead())+`"`, `data-live="/"`)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Rastrillo-Fragment", "1")
	rec := ana.do(req)
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Groceries") || strings.Contains(body, "<html") {
		t.Fatalf("the list as a fragment: %d\n%s", rec.Code, body)
	}
}

// static/live.js is libs/doorbell's, byte for byte: the app vendors it
// because its assets are embedded in its own module.
func TestLiveJSIsTheDoorbells(t *testing.T) {
	t.Parallel()
	vendored, err := os.ReadFile(filepath.Join("..", "myapp", "static", "live.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(vendored) != string(doorbell.LiveJS) {
		t.Fatal("static/live.js differs from libs/doorbell/live.js; copy it again")
	}
}
