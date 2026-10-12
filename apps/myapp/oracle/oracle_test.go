//go:build oracle

// Package oracle_test is the black-box form of myapp's executable
// scenarios (gastos's ADR 0007, step 1): the same Given / When / Then,
// in the customer's words, driven over HTTP against a running process,
// so one suite judges any binary that claims to be myapp, or a
// deployed instance. The in-process scenarios in internal/myapptest
// stay where they are; this suite reads nothing of the app but its
// HTTP surface: the screens (sees) and the change record as
// /changes.json (changed). It is a test package, not a program: one
// module root per app.
//
// Not part of the plain suite, because it execs go build, which Go's
// test cache cannot see:
//
//	make oracle
//
// What it runs against, one fresh process per scenario:
//
//   - by default, ./cmd/myapp, built once per run into a temp dir;
//   - MYAPP_ORACLE_BIN=<path>: that binary instead.
//
// Either is started as `<bin> -addr 127.0.0.1:<port> -db <tmp>/oracle.db`
// with MYAPP_ORIGIN=http://127.0.0.1:<port>, waited on at /healthz and
// stopped when the scenario ends.
//
// MYAPP_ORACLE_DUMP=<dir> writes each scenario's change record there,
// as /changes.json states it, when the scenario ends (dumpWhenDone):
// the input a flow extractor or a comparison between two binaries
// reads.
//
// MYAPP_ORACLE_URL=<base url> drives an instance already running
// instead. A scenario assumes an empty instance, so that is one
// scenario per fresh instance (-run '^TestScenarioX$'); a second
// scenario in the same run fails rather than read another's records.
// Start the instance with MYAPP_ORIGIN set to that same URL.
//
// What the in-process suite has that this one cannot: a write that
// rolls back (TestScenarioARolledBackWriteRingsNothing) is made below
// the HTTP surface, so it has no black-box form.
package oracle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"amadan.net/rastrillo/rastrillo/budget"
)

var (
	tmpDir    string // the run's scratch: the built binary lives here
	buildOnce sync.Once
	binPath   string
	buildErr  error
	urlUsed   atomic.Int32 // scenarios run against MYAPP_ORACLE_URL
)

func TestMain(m *testing.M) {
	d, err := os.MkdirTemp("", "myapp-oracle-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	tmpDir = d
	code := budget.Main(m)
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// binary is the myapp to start: MYAPP_ORACLE_BIN, else ./cmd/myapp
// built once per run (inside the measured time, on purpose: it is
// part of what running the oracle costs).
func binary(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("MYAPP_ORACLE_BIN"); b != "" {
		return b
	}
	buildOnce.Do(func() {
		binPath = filepath.Join(tmpDir, "myapp")
		cmd := exec.Command("go", "build", "-o", binPath, "myapp/cmd/myapp")
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/myapp: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

// boot starts a fresh myapp on a free port over an empty database and
// returns its base URL once /healthz answers. The process is stopped
// when the test ends, and its log is printed if the test failed.
func boot(t *testing.T) string {
	t.Helper()
	bin := binary(t)
	dir := t.TempDir()
	var lastErr error
	for range 3 { // a free port can be taken between asking and listening
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		base := "http://" + addr

		var log bytes.Buffer
		cmd := exec.Command(bin, "-addr", addr, "-db", filepath.Join(dir, "oracle.db"))
		cmd.Env = append(childEnv(), "MYAPP_ORIGIN="+base, "MYAPP_TENANT=oracle")
		cmd.Stdout, cmd.Stderr = &log, &log
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s: %v", bin, err)
		}
		// exited closes when the process is gone; waitErr says how.
		exited := make(chan struct{})
		var waitErr error
		go func() { waitErr = cmd.Wait(); close(exited) }()
		stop := func() {
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				cmd.Process.Kill()
				<-exited
			}
		}
		if lastErr = waitHealthy(base, exited, &waitErr); lastErr == nil {
			t.Cleanup(func() {
				stop()
				if t.Failed() {
					t.Logf("myapp log:\n%s", log.String())
				}
			})
			return base
		}
		stop()
		lastErr = fmt.Errorf("%v\n%s", lastErr, log.String())
	}
	t.Fatalf("myapp did not come up: %v", lastErr)
	return ""
}

// childEnv is this process's environment without what would change
// how myapp behaves: telemetry export, the app's own settings and an
// activation socket.
func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "OTEL_") || strings.HasPrefix(kv, "MYAPP_") || strings.HasPrefix(kv, "LISTEN_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func waitHealthy(base string, exited <-chan struct{}, waitErr *error) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return fmt.Errorf("exited before serving: %v", *waitErr)
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		res, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no healthy /healthz within 10s")
}

// --- Given ---------------------------------------------------------

// scenario is one Given / When / Then against a fresh instance: a
// process of its own over an empty database. Members join by signing
// up, exactly as they would.
type scenario struct {
	t      *testing.T
	base   string
	mark   int64   // the last change id seen; Then reads from here
	reader *person // whose session reads /changes.json: the first to join
}

// person is a member with a browser: a cookie jar that does not
// follow redirects, so a Then can read the 303 itself.
type person struct {
	s      *scenario
	Name   string
	Email  string
	client *http.Client
	member int64 // their member id, once looked up
}

func given(t *testing.T) *scenario {
	t.Helper()
	if base := os.Getenv("MYAPP_ORACLE_URL"); base != "" {
		if urlUsed.Add(1) > 1 {
			t.Fatal("MYAPP_ORACLE_URL is one instance: run one scenario per fresh instance (-run '^TestScenarioX$')")
		}
		return &scenario{t: t, base: strings.TrimRight(base, "/")}
	}
	s := &scenario{t: t, base: boot(t)}
	s.dumpWhenDone()
	return s
}

// dumpWhenDone writes the scenario's whole change record, as
// /changes.json states it, to $MYAPP_ORACLE_DUMP/<test name>.json
// when the scenario ends (before its process is stopped: cleanups run
// last-registered first). Off unless the variable names a directory.
func (s *scenario) dumpWhenDone() {
	dir := os.Getenv("MYAPP_ORACLE_DUMP")
	if dir == "" {
		return
	}
	s.t.Cleanup(func() {
		if s.reader == nil || s.t.Failed() {
			return
		}
		b, err := json.MarshalIndent(s.since(0), "", "  ")
		if err != nil {
			s.t.Errorf("dump: %v", err)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, s.t.Name()+".json"), append(b, '\n'), 0o644); err != nil {
			s.t.Errorf("dump: %v", err)
		}
	})
}

// reply is what came back: status, headers and the whole body.
type reply struct {
	Code   int
	Header http.Header
	Body   string
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *scenario) send(c *http.Client, req *http.Request) reply {
	s.t.Helper()
	res, err := c.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		s.t.Fatalf("%s %s: reading body: %v", req.Method, req.URL.Path, err)
	}
	return reply{Code: res.StatusCode, Header: res.Header, Body: string(b)}
}

func (s *scenario) request(method, target string, body io.Reader) *http.Request {
	s.t.Helper()
	req, err := http.NewRequest(method, s.base+target, body)
	if err != nil {
		s.t.Fatal(err)
	}
	return req
}

// visitor is someone with a browser and no account.
func (s *scenario) visitor() *person {
	return &person{s: s, Name: "a visitor", client: newClient()}
}

// aMember joins through POST /signup and comes back signed in.
// Joining is a business fact: it lands MemberJoined.
func (s *scenario) aMember(name string) *person {
	s.t.Helper()
	p := &person{s: s, Name: name, Email: strings.ToLower(name) + "@example.test", client: newClient()}
	rec := p.post("/signup", url.Values{"name": {name}, "email": {p.Email}, "password": {"correct horse battery"}})
	if rec.Code != http.StatusSeeOther {
		s.t.Fatalf("join as %s: status %d\n%s", name, rec.Code, rec.Body)
	}
	if !p.signedIn() {
		s.t.Fatalf("join as %s: no session cookie", name)
	}
	if s.reader == nil {
		s.reader = p
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

// signedIn is whether the person's browser holds a session cookie.
func (p *person) signedIn() bool {
	u, _ := url.Parse(p.s.base)
	for _, c := range p.client.Jar.Cookies(u) {
		if c.Value != "" {
			return true
		}
	}
	return false
}

// --- When ----------------------------------------------------------

func (p *person) do(req *http.Request) reply {
	p.s.t.Helper()
	return p.s.send(p.client, req)
}

// get is the person looking at a screen.
func (p *person) get(target string) reply {
	p.s.t.Helper()
	return p.do(p.s.request(http.MethodGet, target, nil))
}

// post is the person submitting a form, with the Origin a browser
// sends as its same-origin evidence.
func (p *person) post(target string, form url.Values) reply {
	p.s.t.Helper()
	req := p.s.request(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", p.s.base)
	return p.do(req)
}

// id is the person's member id: the row their MemberJoined landed on.
func (p *person) id() int64 {
	p.s.t.Helper()
	if p.member != 0 {
		return p.member
	}
	for _, c := range p.s.since(0) {
		if c.Reason == ReasonMemberJoined && c.Table == "members" && c.field("Email") == p.Email {
			p.member = c.RowID
			return p.member
		}
	}
	p.s.t.Fatalf("no MemberJoined for %s", p.Email)
	return 0
}

// writesNote is the one way a note gets in: the form.
func (p *person) writesNote(title, body string) reply {
	p.s.t.Helper()
	return p.post("/notes", url.Values{"title": {title}, "body": {body}})
}

// correctsNote puts note id right through its correction form.
func (p *person) correctsNote(id int64, title, body string) reply {
	p.s.t.Helper()
	return p.post("/notes/"+itoa(id)+"/correct", url.Values{"title": {title}, "body": {body}})
}

// note is the id of the note written as title: the row its
// NoteWritten landed on.
func (s *scenario) note(title string) int64 {
	s.t.Helper()
	for _, c := range s.since(0) {
		if c.Reason == ReasonNoteWritten && c.Table == "notes" && c.field("Title") == title {
			return c.RowID
		}
	}
	s.t.Fatalf("no note %q", title)
	return 0
}

// notesHead is the latest change id within any note: what the notes
// list has seen.
func (s *scenario) notesHead() int64 {
	s.t.Helper()
	var head int64
	for _, c := range s.since(0) {
		if strings.HasPrefix(c.Scope, "note:") {
			head = c.ID
		}
	}
	return head
}

// --- Then ----------------------------------------------------------

// change is one record as /changes.json states it.
type change struct {
	ID            int64           `json:"id"`
	Reason        string          `json:"reason"`
	Cause         string          `json:"cause"`
	RequestID     string          `json:"request_id"`
	CorrelationID string          `json:"correlation_id"`
	Actor         string          `json:"actor"`
	OnBehalfOf    string          `json:"on_behalf_of"`
	EntryPoint    string          `json:"entry_point"`
	Scope         string          `json:"scope"`
	Table         string          `json:"table"`
	RowID         int64           `json:"row_id"`
	Op            string          `json:"op"`
	RawDiff       json.RawMessage `json:"diff"`
	CreatedAt     string          `json:"created_at"`
}

func (c change) Ref() string  { return "change:" + itoa(c.ID) }
func (c change) Diff() string { return string(c.RawDiff) }

// field is one string field of the diff, "" when absent.
func (c change) field(name string) string {
	var m map[string]any
	if json.Unmarshal(c.RawDiff, &m) != nil {
		return ""
	}
	v, _ := m[name].(string)
	return v
}

// since reads every change after id, oldest first, paging on until
// the feed comes back empty.
func (s *scenario) since(after int64) []change {
	s.t.Helper()
	if s.reader == nil {
		s.t.Fatal("nobody has joined, so nobody can read the change log")
	}
	var all []change
	for {
		rec := s.reader.get("/changes.json?after=" + itoa(after))
		if rec.Code != http.StatusOK {
			s.t.Fatalf("GET /changes.json: status %d\n%s", rec.Code, rec.Body)
		}
		var feed struct {
			Changes []change `json:"changes"`
		}
		if err := json.Unmarshal([]byte(rec.Body), &feed); err != nil {
			s.t.Fatalf("GET /changes.json: %v\n%s", err, rec.Body)
		}
		if len(feed.Changes) == 0 {
			return all
		}
		all = append(all, feed.Changes...)
		after = feed.Changes[len(feed.Changes)-1].ID
	}
}

// mark_ remembers where the change log is, so the next changed() reads
// only what the When landed.
func (s *scenario) mark_() {
	s.t.Helper()
	if got := s.since(s.mark); len(got) > 0 {
		s.mark = got[len(got)-1].ID
	}
}

// changed asserts the reasons that landed since the mark, in order,
// and that none bled into another context's table.
func (s *scenario) changed(reasons ...string) []change {
	s.t.Helper()
	got := s.since(s.mark)
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
		p.s.t.Fatalf("%s GET %s: status %d\n%s", p.Name, target, rec.Code, rec.Body)
	}
	for _, f := range fragments {
		if !strings.Contains(rec.Body, f) {
			p.s.t.Fatalf("%s GET %s: missing %q in\n%s", p.Name, target, f, rec.Body)
		}
	}
}

func redirected(t *testing.T, rec reply, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header.Get("Location") != to {
		t.Fatalf("want 303 to %s, got %d to %q\n%s", to, rec.Code, rec.Header.Get("Location"), rec.Body)
	}
}

// noBleed holds "one table, one vocabulary" against the oracle's own
// copy of which context owns what (vocabulary_test.go): every
// change's reason belongs to the context that owns its table.
func noBleed(t *testing.T, got []change) {
	t.Helper()
	for _, c := range got {
		if r, e := reasonContext[c.Reason], tableContext[c.Table]; r == "" || r != e {
			t.Fatalf("context bleed: %s (%q) landed on %s (%q) in change #%d", c.Reason, r, c.Table, e, c.ID)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
