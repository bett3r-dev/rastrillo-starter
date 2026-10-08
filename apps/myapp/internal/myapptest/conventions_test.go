package myapptest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	myapp "myapp/internal/myapp"
	"myproject/libs/changes"
)

// The written conventions of the change record, held mechanically.
// Each test here names the failure it prevents. They are app-agnostic
// apart from the example's verbs (writesNote, correctsNote): keep
// them when the example goes, repointed at the app's own.

// A rebuild migration drops a table's triggers with the table. If one
// ever rebuilds changes, it must re-create the append-only pair; this
// proves they are there.
func TestChangesIsStillAppendOnly(t *testing.T) {
	t.Parallel()
	s := given(t)
	s.aMember("Ana")
	if err := s.db.G.Exec("UPDATE changes SET reason = 'Rewritten'").Error; err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE on changes: err = %v, want the append-only trigger", err)
	}
	if err := s.db.G.Exec("DELETE FROM changes").Error; err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE on changes: err = %v, want the append-only trigger", err)
	}
	var n int64
	s.db.G.Model(&changes.Change{}).Count(&n)
	if n != 1 {
		t.Fatalf("changes rows = %d, want the MemberJoined still there", n)
	}
}

// request_id is the root of the chain, on every link: a correction
// made in a later request still names the request that wrote the
// note, not its own.
func TestEveryChangeCarriesItsRequest(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	s.mark_()
	redirected(t, ana.writesNote("Groceries", "Milk"), "/")
	written := s.changed(myapp.ReasonNoteWritten)[0]
	if written.RequestID != written.Cause || !strings.HasPrefix(written.RequestID, "request:") {
		t.Fatalf("root change: request_id %q, cause %q", written.RequestID, written.Cause)
	}
	redirected(t, ana.correctsNote(s.note("Groceries").ID, "Groceries", "Milk and bread"), "/")
	corrected := s.changed(myapp.ReasonNoteCorrected)[0]
	if corrected.Cause != written.Ref() || corrected.RequestID != written.RequestID {
		t.Fatalf("chained change: cause %q request_id %q, want %q / %q", corrected.Cause, corrected.RequestID, written.Ref(), written.RequestID)
	}
}

// A change's correlation is the trace id of the request it ran in,
// read off the write's context rather than handed along: a person's
// form post carries the id its response shows, and a request made
// inside another service's trace carries THAT trace's id.
func TestEveryChangeCarriesTheTraceOfItsRequest(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")

	s.mark_()
	rec := ana.writesNote("Groceries", "Milk")
	redirected(t, rec, "/")
	id := rec.Header().Get("X-Correlation-Id")
	got := s.changed(myapp.ReasonNoteWritten)
	if got[0].CorrelationID != id || got[0].Cause != "request:"+id {
		t.Fatalf("change correlation %q cause %q, want the response's %q", got[0].CorrelationID, got[0].Cause, id)
	}

	// The same form, posted from inside an upstream trace.
	const upstream = "4bf92f3577b34da6a3ce929d0e0e4736"
	form := url.Values{"title": {"Rent"}, "body": {"Due on the 10th"}}
	req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("traceparent", "00-"+upstream+"-00f067aa0ba902b7-01")
	redirected(t, ana.do(req), "/")
	got = s.changed(myapp.ReasonNoteWritten)
	if got[0].CorrelationID != upstream {
		t.Fatalf("correlation %q, want the upstream trace %q", got[0].CorrelationID, upstream)
	}
}

// The database half of "every tracked write is explained", on the
// real app: raw SQL on a tracked table is refused by the guard
// trigger, and the password hash, the one exempt column, still lands.
func TestRawSQLOnTrackedTablesIsRefused(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	var m myapp.Member
	s.db.G.First(&m, ana.id())
	if m.PasswordHash == "" {
		t.Fatal("the exempt hash write did not land")
	}
	s.mark_()
	if err := s.db.G.Exec("UPDATE members SET name = 'Eve' WHERE id = ?", m.ID).Error; err == nil || !strings.Contains(err.Error(), "unexplained") {
		t.Fatalf("raw rename: err = %v, want the guard", err)
	}
	if err := s.db.G.Exec("INSERT INTO notes (author_id, title, created_at, updated_at) VALUES (1, 'raw', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)").Error; err == nil || !strings.Contains(err.Error(), "unexplained") {
		t.Fatalf("raw note: err = %v, want the guard", err)
	}
	s.changed()
}

// Every table is tracked or declared untracked with a reason, never
// neither and never both. The failure this prevents: a new table
// whose writes nobody decided to explain.
func TestEveryTableIsTrackedOrDeclaredNot(t *testing.T) {
	t.Parallel()
	s := given(t)
	var tables []string
	if err := s.db.G.Raw("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	tracked := map[string]bool{}
	for _, n := range myapp.Tracked {
		tracked[n] = true
	}
	for _, n := range tables {
		_, untracked := myapp.Untracked[n]
		switch {
		case tracked[n] && untracked:
			t.Errorf("%s is in both Tracked and Untracked", n)
		case !tracked[n] && !untracked:
			t.Errorf("%s is in neither Tracked nor Untracked: decide, and say why", n)
		}
	}
	for n := range myapp.Untracked {
		if !slices.Contains(tables, n) {
			t.Errorf("Untracked names %s, which does not exist", n)
		}
	}
	for _, n := range myapp.Tracked {
		if !slices.Contains(tables, n) {
			t.Errorf("Tracked names %s, which does not exist", n)
		}
	}
}

// Reason names are facts in the business's words. The failure this
// prevents: the vocabulary degrading into a modified-at timestamp.
// Refused: Updated, Edited, Modified, Other, Generic as whole words
// anywhere; Changed only when what changed is named with at least
// two words before it (BillSplitChanged says what; BillChanged does
// not).
func TestReasonsAreFactsNotTimestamps(t *testing.T) {
	t.Parallel()
	words := regexp.MustCompile(`[A-Z][a-z0-9]*`)
	banned := map[string]bool{"Updated": true, "Edited": true, "Modified": true, "Other": true, "Generic": true}
	for _, r := range myapp.Reasons {
		ws := words.FindAllString(r, -1)
		if strings.Join(ws, "") != r {
			t.Errorf("%s is not CamelCase words", r)
			continue
		}
		for _, w := range ws {
			if banned[w] {
				t.Errorf("%s: %q says what the code did, not what happened", r, w)
			}
		}
		if ws[len(ws)-1] == "Changed" && len(ws) < 3 {
			t.Errorf("%s: Changed without saying what changed; name the fact (BillSplitChanged, not BillChanged)", r)
		}
	}
}

// Every chained change points at an earlier one, so the chain always
// descends to a root; no loop can be built from existing rows.
func TestChainsDescend(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana := s.aMember("Ana")
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	redirected(t, ana.correctsNote(s.note("Groceires").ID, "Groceries", "Milk"), "/")
	redirected(t, ana.correctsNote(s.note("Groceries").ID, "Groceries", "Milk and bread"), "/")
	var all []changes.Change
	s.db.G.Order("id").Find(&all)
	byRef := map[string]changes.Change{}
	for _, c := range all {
		byRef[c.Ref()] = c
	}
	for _, c := range all {
		if !strings.HasPrefix(c.Cause, "change:") {
			if c.RequestID != c.Cause {
				t.Errorf("#%d root: request_id %q, cause %q", c.ID, c.RequestID, c.Cause)
			}
			continue
		}
		p, ok := byRef[c.Cause]
		if !ok || p.ID >= c.ID {
			t.Errorf("#%d caused by %s, which is not an earlier change", c.ID, c.Cause)
		}
		if p.RequestID != c.RequestID {
			t.Errorf("#%d request_id %q differs from its cause's %q", c.ID, c.RequestID, p.RequestID)
		}
	}
}

// The fold: replaying every row's diffs reproduces the live row. On
// an instance where people joined, wrote and corrected, the record
// explains every row and nothing else; a raw write that slipped past
// the hook and the guard is what this would name.
func TestTheRecordFoldsToTheLiveData(t *testing.T) {
	t.Parallel()
	s := given(t)
	ana, luis := s.aMember("Ana"), s.aMember("Luis")
	redirected(t, ana.writesNote("Groceires", "Milk"), "/")
	redirected(t, luis.writesNote("Rent", "Due on the 10th"), "/")
	redirected(t, luis.correctsNote(s.note("Groceires").ID, "Groceries", "Milk"), "/")
	ms, err := changes.Verify(s.db.G, myapp.Tracked, myapp.TrackedExempt)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		t.Error(m)
	}
	var all []changes.Change
	s.db.G.Order("id").Find(&all)
	noBleed(t, all)
}

// Every table belongs to exactly one context or is plumbing, and
// every reason to exactly one context. The failure this prevents: a
// table or a reason nobody placed.
func TestEveryTableAndReasonHasOneContext(t *testing.T) {
	t.Parallel()
	owner := map[string]string{}
	for ctx, tables := range myapp.Contexts {
		for _, tb := range tables {
			if prev, dup := owner[tb]; dup {
				t.Errorf("%s is in both %s and %s", tb, prev, ctx)
			}
			owner[tb] = ctx
		}
	}
	for _, tb := range myapp.Tracked {
		if owner[tb] == "" {
			t.Errorf("tracked table %s belongs to no context", tb)
		}
	}
	for tb := range myapp.Untracked {
		if owner[tb] != "" {
			t.Errorf("plumbing table %s is claimed by %s", tb, owner[tb])
		}
	}
	for _, r := range myapp.Reasons {
		if myapp.ContextOfReason(r) == "" {
			t.Errorf("reason %s belongs to no context", r)
		}
	}
}
