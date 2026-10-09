package myapptest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/db"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
)

// The scenario vocabulary. A scenario is one Given / When / Then in
// the business's words, through the app's real entry points:
//
//   - Given starts from an empty instance; members join through
//     POST /signup, exactly as they would.
//   - When is a form POST by a person (or an agent's POST under /agent/).
//   - Then asserts the screen (sees) AND the change records that
//     landed, by reason (changed).
//
// Write the scenario before the handler. The domain verbs at the end
// of the When section (writesNote, correctsNote) are the example
// app's; replace them with your own, keep the rest.

// scenario is one Given / When / Then against a fresh instance.
type scenario struct {
	t    *testing.T
	h    http.Handler
	db   *db.DB
	mark int64 // the last change id seen; Then reads from here
}

// person is a member with a browser: their session cookie.
type person struct {
	s      *scenario
	Name   string
	Email  string
	cookie *http.Cookie
}

func given(t *testing.T) *scenario {
	t.Helper()
	h, d := newApp(t)
	collectFlow(t, d)
	return &scenario{t: t, h: h, db: d}
}

// aMember joins through POST /signup and comes back signed in.
// Joining is a business fact: it lands MemberJoined.
func (s *scenario) aMember(name string) *person {
	s.t.Helper()
	email := strings.ToLower(name) + "@example.test"
	rec := post(s.t, s.h, "/signup", url.Values{"name": {name}, "email": {email}, "password": {"correct horse battery"}})
	if rec.Code != http.StatusSeeOther {
		s.t.Fatalf("join as %s: status %d\n%s", name, rec.Code, rec.Body.String())
	}
	p := &person{s: s, Name: name, Email: email}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 && c.Value != "" {
			p.cookie = c
		}
	}
	if p.cookie == nil {
		s.t.Fatalf("join as %s: no session cookie", name)
	}
	return p
}

// members joins several people in one line, in order (ids ascend).
func (s *scenario) members(names ...string) []*person {
	out := make([]*person, 0, len(names))
	for _, n := range names {
		out = append(out, s.aMember(n))
	}
	return out
}

// --- When ---------------------------------------------------------

func (p *person) do(req *http.Request) *httptest.ResponseRecorder {
	req.AddCookie(p.cookie)
	rec := httptest.NewRecorder()
	p.s.h.ServeHTTP(rec, req)
	return rec
}

// get is the person looking at a screen.
func (p *person) get(target string) *httptest.ResponseRecorder {
	p.s.t.Helper()
	return p.do(httptest.NewRequest(http.MethodGet, target, nil))
}

// post is the person submitting a form.
func (p *person) post(target string, form url.Values) *httptest.ResponseRecorder {
	p.s.t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", testOrigin)
	return p.do(req)
}

// id is the person's member id.
func (p *person) id() int64 {
	var m myapp.Member
	if err := p.s.db.G.Where("email = ?", p.Email).First(&m).Error; err != nil {
		p.s.t.Fatalf("member %s: %v", p.Name, err)
	}
	return m.ID
}

// writesNote is the one way a note gets in: the form.
func (p *person) writesNote(title, body string) *httptest.ResponseRecorder {
	p.s.t.Helper()
	return p.post("/notes", url.Values{"title": {title}, "body": {body}})
}

// correctsNote puts note id right through its correction form.
func (p *person) correctsNote(id int64, title, body string) *httptest.ResponseRecorder {
	p.s.t.Helper()
	return p.post("/notes/"+itoa(id)+"/correct", url.Values{"title": {title}, "body": {body}})
}

// note is the note titled title, for a When that needs its id.
func (s *scenario) note(title string) myapp.Note {
	s.t.Helper()
	var n myapp.Note
	if err := s.db.G.Where("title = ?", title).First(&n).Error; err != nil {
		s.t.Fatalf("note %q: %v", title, err)
	}
	return n
}

// --- Then ---------------------------------------------------------

// mark_ remembers where the change log is, so the next changed() reads
// only what the When landed.
func (s *scenario) mark_() {
	var last changes.Change
	s.db.G.Order("id DESC").First(&last)
	s.mark = last.ID
}

// changed asserts the reasons that landed since the mark, in order.
// A scenario states what happened in the business's words, and the
// change records are held to it.
func (s *scenario) changed(reasons ...string) []changes.Change {
	s.t.Helper()
	var got []changes.Change
	s.db.G.Where("id > ?", s.mark).Order("id").Find(&got)
	var names []string
	for _, c := range got {
		names = append(names, c.Reason)
	}
	if strings.Join(names, ",") != strings.Join(reasons, ",") {
		s.t.Fatalf("changes since mark:\n  got  %v\n  want %v", names, reasons)
	}
	noBleed(s.t, got)
	if len(got) > 0 {
		s.mark = got[len(got)-1].ID
	}
	return got
}

// sees asserts the screen a person gets shows every fragment.
func (p *person) sees(target string, fragments ...string) {
	p.s.t.Helper()
	rec := p.get(target)
	if rec.Code != http.StatusOK {
		p.s.t.Fatalf("%s GET %s: status %d\n%s", p.Name, target, rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, f := range fragments {
		if !strings.Contains(body, f) {
			p.s.t.Fatalf("%s GET %s: missing %q in\n%s", p.Name, target, f, body)
		}
	}
}

func redirected(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Fatalf("want 303 to %s, got %d to %q\n%s", to, rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// noBleed holds "one table, one vocabulary": every change's reason
// belongs to the context that owns the table it landed on. A context
// that needs another's data to move causes a change there.
func noBleed(t *testing.T, got []changes.Change) {
	t.Helper()
	for _, c := range got {
		if r, e := myapp.ContextOfReason(c.Reason), myapp.ContextOfTable(c.Entity); r != e {
			t.Fatalf("context bleed: %s (%s) landed on %s (%s) in change #%d", c.Reason, r, c.Entity, e, c.ID)
		}
	}
}
