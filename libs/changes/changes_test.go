package changes_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"
	"amadan.net/rastrillo/rastrillo/migrate"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"myproject/libs/changes"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

type probe struct {
	ID    int64
	Name  string
	Cents int64
}

// open is a throwaway database for this package alone: it has no
// migrate.Set of its own, so AutoMigrate stands in for the template
// copy the app's harness uses.
func open(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &probe{}); err != nil {
		t.Fatal(err)
	}
	if err := changes.Install(d.G, changes.Policy{Tracked: []string{"probes"}, Exempt: map[string][]string{"probes": {"cents"}}, Correlation: correlation}); err != nil {
		t.Fatal(err)
	}
	return d.G
}

// The app reads the correlation off the trace in the context; this
// package only needs some value the context carries, so its tests
// stand one in.
type requestKey struct{}

func inRequest(id string) context.Context {
	return context.WithValue(context.Background(), requestKey{}, id)
}

func correlation(ctx context.Context) string {
	id, _ := ctx.Value(requestKey{}).(string)
	return id
}

// A reason is not enough: a write must also run inside a request,
// or nothing could tie it back to what started it.
func TestUncorrelatedWriteIsRefused(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("member:1").Because("ProbeRecorded", "")
	err := g.WithContext(tr.Context(context.Background())).Create(&probe{Name: "x"}).Error
	if !errors.Is(err, changes.ErrUncorrelated) {
		t.Fatalf("err = %v, want ErrUncorrelated", err)
	}
	var n int64
	g.Model(&probe{}).Count(&n)
	if n != 0 {
		t.Fatalf("the refused row landed: %d probes", n)
	}
}

func TestUnexplainedWriteIsRefusedAndRolledBack(t *testing.T) {
	t.Parallel()
	g := open(t)
	err := g.Create(&probe{Name: "x"}).Error
	if !errors.Is(err, changes.ErrUnexplained) {
		t.Fatalf("err = %v, want ErrUnexplained", err)
	}
	var n int64
	g.Model(&probe{}).Count(&n)
	if n != 0 {
		t.Fatalf("the refused row landed: %d probes", n)
	}
}

func TestExplainedWriteLandsARowAndTheChange(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("human").Because("ProbeRecorded", "")
	ctx := tr.Context(inRequest("req-1"))
	p := probe{Name: "x", Cents: 4530}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	var got []changes.Change
	g.Find(&got)
	if len(got) != 1 {
		t.Fatalf("changes = %d, want 1", len(got))
	}
	c := got[0]
	if c.Reason != "ProbeRecorded" || c.Entity != "probes" || c.EntityID != p.ID || c.Op != "create" ||
		c.Cause != "request:req-1" || c.CorrelationID != "req-1" || c.Actor != "human" {
		t.Fatalf("change = %+v", c)
	}
	if c.Diff == "" || c.Diff[0] != '{' {
		t.Fatalf("diff = %q", c.Diff)
	}
	last, ok := tr.Last()
	if !ok || last.ID != c.ID {
		t.Fatalf("Last() = %+v %v", last, ok)
	}
}

// A Trail opened with no entry point name records the request's
// method and route pattern, so an unnamed action still shows; a named
// one keeps its name.
func TestUnnamedEntryPointRecordsTheRoute(t *testing.T) {
	t.Parallel()
	g := open(t)
	rc := chi.NewRouteContext()
	rc.RouteMethod = "POST"
	rc.RoutePatterns = []string{"/notes/{id}/archive"}
	routed := context.WithValue(inRequest("req-1"), chi.RouteCtxKey, rc)

	unnamed := changes.Begin("member:1").Because("ProbeRecorded", "")
	if err := g.WithContext(unnamed.Context(routed)).Create(&probe{Name: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	named := changes.Begin("member:1").Because("ProbeRecorded", "")
	named.EntryPoint = "ArchiveNote"
	if err := g.WithContext(named.Context(routed)).Create(&probe{Name: "y"}).Error; err != nil {
		t.Fatal(err)
	}
	var got []changes.Change
	g.Order("id").Find(&got)
	if len(got) != 2 || got[0].EntryPoint != "POST /notes/{id}/archive" || got[1].EntryPoint != "ArchiveNote" {
		t.Fatalf("changes = %+v", got)
	}
}

// A platform admin acting as a member (ADR 0005) is recorded as both:
// actor stays who did it, on_behalf_of says as whom. A Trail that
// acts as itself leaves on_behalf_of empty.
func TestAWriteOnBehalfOfAMemberRecordsBoth(t *testing.T) {
	t.Parallel()
	g := open(t)
	as := changes.Begin("platform:7").Because("ProbeRecorded", "")
	as.OnBehalfOf = "member:12"
	if err := g.WithContext(as.Context(inRequest("req-1"))).Create(&probe{Name: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	itself := changes.Begin("member:12").Because("ProbeRecorded", "")
	if err := g.WithContext(itself.Context(inRequest("req-2"))).Create(&probe{Name: "y"}).Error; err != nil {
		t.Fatal(err)
	}
	var got []changes.Change
	g.Order("id").Find(&got)
	if len(got) != 2 {
		t.Fatalf("changes = %d, want 2", len(got))
	}
	if got[0].Actor != "platform:7" || got[0].OnBehalfOf != "member:12" {
		t.Fatalf("acting as a member: actor %q, on_behalf_of %q", got[0].Actor, got[0].OnBehalfOf)
	}
	if got[1].Actor != "member:12" || got[1].OnBehalfOf != "" {
		t.Fatalf("acting as itself: actor %q, on_behalf_of %q", got[1].Actor, got[1].OnBehalfOf)
	}
	if r, _ := as.Last(); r.OnBehalfOf != "member:12" {
		t.Fatalf("Last() on_behalf_of = %q", r.OnBehalfOf)
	}
}

// The migrations, not AutoMigrate, are what every app boots with:
// they must give changes the on_behalf_of and scope columns (scope
// indexed), empty for rows recorded before them, and the nullable rule
// column, NULL for them; and keep the table append-only, the rule
// column included.
func TestSchemaAddsItsColumnsAndStaysAppendOnly(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "s.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := migrate.Apply(context.Background(), d, changes.Schema); err != nil {
		t.Fatal(err)
	}
	if err := d.G.Exec("INSERT INTO changes (reason, cause, request_id, correlation_id, actor, entity, entity_id, op, diff, created_at) " +
		"VALUES ('ProbeRecorded', 'request:r', 'request:r', 'r', 'human', 'probes', 1, 'create', '{}', CURRENT_TIMESTAMP)").Error; err != nil {
		t.Fatal(err)
	}
	var c changes.Change
	if err := d.G.First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.OnBehalfOf != "" || c.EntryPoint != "" || c.Scope != "" {
		t.Fatalf("defaults: on_behalf_of %q, entry_point %q, scope %q", c.OnBehalfOf, c.EntryPoint, c.Scope)
	}
	var idx int64
	d.G.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_changes_scope'").Scan(&idx)
	if idx != 1 {
		t.Fatal("the migrations left changes.scope unindexed")
	}
	var notNull []int
	if err := d.G.Raw("SELECT \"notnull\" FROM pragma_table_info('changes') WHERE name = 'rule'").Scan(&notNull).Error; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(notNull, []int{0}) {
		t.Fatalf("changes.rule notnull = %v, want one nullable column", notNull)
	}
	var unruled int64
	if err := d.G.Raw("SELECT count(*) FROM changes WHERE rule IS NULL").Scan(&unruled).Error; err != nil {
		t.Fatal(err)
	}
	if unruled != 1 {
		t.Fatalf("%d rows with a NULL rule, want the row recorded without one", unruled)
	}
	for _, stmt := range []string{
		"UPDATE changes SET on_behalf_of = 'member:1'",
		"UPDATE changes SET rule = 'EveryNewMemberIsWelcomed'",
		"DELETE FROM changes",
	} {
		if err := d.G.Exec(stmt).Error; err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err = %v, want the append-only trigger", stmt, err)
		}
	}
}

func TestUpdateAndDeleteRecordTheirOwnReason(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("agent:bank").Because("ProbeRecorded", "")
	ctx := tr.Context(inRequest("req-2"))
	p := probe{Name: "x"}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	first, _ := tr.Last()
	tr.Because("ProbeRenamed", first.Ref())
	if err := g.WithContext(ctx).Model(&p).Select("Name").Updates(map[string]any{"Name": "y"}).Error; err != nil {
		t.Fatal(err)
	}
	tr.Because("ProbeDropped", "")
	if err := g.WithContext(ctx).Delete(&p).Error; err != nil {
		t.Fatal(err)
	}
	rec := tr.Recorded()
	if len(rec) != 3 {
		t.Fatalf("recorded %d, want 3", len(rec))
	}
	if rec[1].Op != "update" || rec[1].Cause != first.Ref() || rec[1].Diff != `{"Name":"y"}` {
		t.Fatalf("update change = %+v", rec[1])
	}
	if rec[2].Op != "delete" || rec[2].EntityID != p.ID {
		t.Fatalf("delete change = %+v", rec[2])
	}
}

func TestRefusalInsideATransactionRollsEverythingBack(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("human").Because("ProbeRecorded", "")
	err := g.WithContext(tr.Context(inRequest("req-3"))).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&probe{Name: "ok"}).Error; err != nil {
			return err
		}
		// A write with the reason cleared mid-transaction.
		tr.Because("", "")
		return tx.Create(&probe{Name: "unexplained"}).Error
	})
	if !errors.Is(err, changes.ErrUnexplained) {
		t.Fatalf("err = %v", err)
	}
	var probes, chs int64
	g.Model(&probe{}).Count(&probes)
	g.Model(&changes.Change{}).Count(&chs)
	if probes != 0 || chs != 0 {
		t.Fatalf("partial state landed: %d probes, %d changes", probes, chs)
	}
}

// The database half of the rule: a write that never met the hook
// (raw SQL, a future ORM) meets the trigger instead.
func TestRawSQLOnATrackedTableIsRefused(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("member:1").Because("ProbeRecorded", "")
	p := probe{Name: "x"}
	if err := g.WithContext(tr.Context(inRequest("req-raw"))).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"INSERT INTO probes (name) VALUES ('raw')",
		"UPDATE probes SET name = 'raw'",
		"DELETE FROM probes",
	} {
		err := g.Exec(stmt).Error
		if err == nil || !strings.Contains(err.Error(), "unexplained") {
			t.Fatalf("%s: err = %v, want the guard trigger", stmt, err)
		}
	}
	// The exempt column alone may be written unexplained; that is the
	// password-hash shape.
	if err := g.Exec("UPDATE probes SET cents = 5 WHERE id = ?", p.ID).Error; err != nil {
		t.Fatalf("exempt column: %v", err)
	}
	var got probe
	g.First(&got, p.ID)
	if got.Name != "x" || got.Cents != 5 {
		t.Fatalf("after the refusals: %+v", got)
	}
}

// A cause chain cannot loop: a "change:<id>" cause must name a change
// that has already landed, and ids only ascend, so every chain
// descends to a root. The one way to build a loop is to forge a
// reference to a change that does not exist yet, and that is refused
// before anything lands. A chain that revisits the same row is
// legitimate (a reminder caused by the unmatched month it nags
// about, a correction caused by the change it corrects) and stays
// allowed.
func TestAForgedCauseIsRefusedSoChainsCannotLoop(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("member:1").Because("ProbeRecorded", "change:999")
	err := g.WithContext(tr.Context(inRequest("req-loop"))).Create(&probe{Name: "x"}).Error
	if !errors.Is(err, changes.ErrBadCause) || !strings.Contains(err.Error(), "change:999") || !strings.Contains(err.Error(), "probes") {
		t.Fatalf("err = %v, want ErrBadCause naming change:999 and probes", err)
	}
	var n int64
	g.Model(&probe{}).Count(&n)
	if n != 0 {
		t.Fatalf("the refused row landed: %d probes", n)
	}

	// Same row twice on one chain: allowed, and the root is carried.
	tr = changes.Begin("member:1").Because("ProbeRecorded", "")
	ctx := tr.Context(inRequest("req-again"))
	p := probe{Name: "y"}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	first, _ := tr.Last()
	tr.Because("ProbeRenamedBecauseOfItself", first.Ref())
	if err := g.WithContext(ctx).Model(&p).Select("Name").Updates(map[string]any{"Name": "z"}).Error; err != nil {
		t.Fatal(err)
	}
	rec := tr.Recorded()
	if len(rec) != 2 || rec[1].Cause != first.Ref() || rec[1].RequestID != "request:req-again" {
		t.Fatalf("recorded = %+v", rec)
	}
	// And across requests: a cause from an earlier request resolves to
	// that request's root, by lookup.
	later := changes.Begin("member:2").Because("ProbeRenamedAgain", rec[1].Ref())
	if err := g.WithContext(later.Context(inRequest("req-later"))).Model(&p).Select("Name").Updates(map[string]any{"Name": "w"}).Error; err != nil {
		t.Fatal(err)
	}
	if got, _ := later.Last(); got.RequestID != "request:req-again" {
		t.Fatalf("cross-request root = %q, want request:req-again", got.RequestID)
	}
}

// What a diff carries, by op: the whole row on create (every field,
// zero values included, so a fold starts from nothing), the written
// fields on update, the row as loaded on delete. op says which; the
// diff never needs a from/to envelope. A delete with no reason is
// refused like any other write, and the row stays.
func TestDiffCarriesTheRowByOp(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("member:1").Because("ProbeRecorded", "")
	ctx := tr.Context(inRequest("req-diff"))
	p := probe{Name: "x"}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	tr.Because("ProbeRenamed", "")
	if err := g.WithContext(ctx).Model(&p).Select("Name").Updates(map[string]any{"Name": "y"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.Delete(&p).Error; !errors.Is(err, changes.ErrUnexplained) {
		t.Fatalf("bare delete: err = %v, want ErrUnexplained", err)
	}
	var still int64
	g.Model(&probe{}).Where("id = ?", p.ID).Count(&still)
	if still != 1 {
		t.Fatal("the bare delete went through")
	}
	var loaded probe
	g.First(&loaded, p.ID)
	tr.Because("ProbeDropped", "")
	if err := g.WithContext(ctx).Delete(&loaded).Error; err != nil {
		t.Fatal(err)
	}
	rec := tr.Recorded()
	want := []struct{ op, diff string }{
		{"create", `{"ID":1,"Name":"x","Cents":0}`},
		{"update", `{"Name":"y"}`},
		{"delete", `{"ID":1,"Name":"y","Cents":0}`},
	}
	if len(rec) != len(want) {
		t.Fatalf("recorded %d changes, want %d: %+v", len(rec), len(want), rec)
	}
	for i, w := range want {
		if rec[i].Op != w.op || rec[i].Diff != w.diff {
			t.Errorf("#%d: op %s diff %s, want %s %s", i, rec[i].Op, rec[i].Diff, w.op, w.diff)
		}
	}
}

// A scoped Trail's every change carries the scope, whatever the op:
// a delete and an update diff, which carry no session column of their
// own, included. An unscoped Trail's carry "".
func TestAScopedTrailScopesEveryChange(t *testing.T) {
	t.Parallel()
	g := open(t)
	tr := changes.Begin("member:1").Within("session:12").Because("ProbeRecorded", "")
	ctx := tr.Context(inRequest("req-s"))
	p := probe{Name: "x"}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	tr.Because("ProbeRenamed", "")
	if err := g.WithContext(ctx).Model(&p).Select("Name").Updates(map[string]any{"Name": "y"}).Error; err != nil {
		t.Fatal(err)
	}
	tr.Because("ProbeDropped", "")
	if err := g.WithContext(ctx).Delete(&p).Error; err != nil {
		t.Fatal(err)
	}
	plain := changes.Begin("member:1").Because("ProbeRecorded", "")
	if err := g.WithContext(plain.Context(inRequest("req-u"))).Create(&probe{Name: "z"}).Error; err != nil {
		t.Fatal(err)
	}
	var got []changes.Change
	g.Order("id").Find(&got)
	if len(got) != 4 {
		t.Fatalf("changes = %d, want 4", len(got))
	}
	for i, op := range []string{"create", "update", "delete"} {
		if got[i].Op != op || got[i].Scope != "session:12" {
			t.Errorf("#%d: op %s scope %q, want %s in session:12", i, got[i].Op, got[i].Scope, op)
		}
	}
	if got[3].Scope != "" || plain.Scope() != "" {
		t.Errorf("unscoped Trail: change scope %q, Trail scope %q", got[3].Scope, plain.Scope())
	}
	if tr.Scope() != "session:12" {
		t.Errorf("Scope() = %q", tr.Scope())
	}
}

// A scope is "kind:id". Anything else, and a second Within that names
// another scope, refuses every write of the Trail before it lands,
// with a named error; a later Within cannot undo it. Within again with
// the same scope is harmless.
func TestAMalformedOrConflictingScopeIsRefused(t *testing.T) {
	t.Parallel()
	g := open(t)
	cases := []struct {
		name   string
		within []string
		want   error
	}{
		{"empty", []string{""}, changes.ErrBadScope},
		{"no id", []string{"session:"}, changes.ErrBadScope},
		{"no kind", []string{":12"}, changes.ErrBadScope},
		{"no colon", []string{"session12"}, changes.ErrBadScope},
		{"upper kind", []string{"Session:12"}, changes.ErrBadScope},
		{"whitespace", []string{"session:1 2"}, changes.ErrBadScope},
		{"another scope", []string{"session:12", "session:13"}, changes.ErrScopeConflict},
		{"not undone", []string{"session:12", "session:13", "session:12"}, changes.ErrScopeConflict},
	}
	for _, c := range cases {
		tr := changes.Begin("member:1")
		for _, s := range c.within {
			tr.Within(s)
		}
		tr.Because("ProbeRecorded", "")
		err := g.WithContext(tr.Context(inRequest("req-bad"))).Create(&probe{Name: c.name}).Error
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	var n int64
	g.Model(&probe{}).Count(&n)
	if n != 0 {
		t.Fatalf("a refused row landed: %d probes", n)
	}

	same := changes.Begin("member:1").Within("session:12").Within("session:12").Because("ProbeRecorded", "")
	if err := g.WithContext(same.Context(inRequest("req-same"))).Create(&probe{Name: "ok"}).Error; err != nil {
		t.Fatalf("the same scope twice: %v", err)
	}
	if r, _ := same.Last(); r.Scope != "session:12" {
		t.Fatalf("scope = %q", r.Scope)
	}
}

// InScope is "this session's changes since cursor X": only that
// scope's, in id order, strictly after the cursor, across requests.
// An empty scope is refused, not read as every unscoped change.
func TestInScopeReadsAScopesChangesAfterACursor(t *testing.T) {
	t.Parallel()
	g := open(t)
	write := func(scope, req string) changes.Change {
		t.Helper()
		tr := changes.Begin("member:1")
		if scope != "" {
			tr.Within(scope)
		}
		tr.Because("ProbeRecorded", "")
		if err := g.WithContext(tr.Context(inRequest(req))).Create(&probe{Name: req}).Error; err != nil {
			t.Fatal(err)
		}
		c, _ := tr.Last()
		return c
	}
	a1 := write("session:1", "r1")
	write("session:2", "r2")
	write("", "r3")
	a2 := write("session:1", "r4")
	a3 := write("session:1", "r5")

	ids := func(cs []changes.Change) []int64 {
		var out []int64
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}
	all, err := changes.InScope(context.Background(), g, "session:1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), []int64{a1.ID, a2.ID, a3.ID}; !slices.Equal(got, want) {
		t.Fatalf("InScope(session:1, 0) = %v, want %v", got, want)
	}
	since, err := changes.InScope(context.Background(), g, "session:1", a2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(since), []int64{a3.ID}; !slices.Equal(got, want) {
		t.Fatalf("InScope(session:1, %d) = %v, want %v", a2.ID, got, want)
	}
	none, err := changes.InScope(context.Background(), g, "session:1", a3.ID)
	if err != nil || len(none) != 0 {
		t.Fatalf("InScope past the last change = %v, %v", ids(none), err)
	}
	if _, err := changes.InScope(context.Background(), g, "", 0); !errors.Is(err, changes.ErrBadScope) {
		t.Fatalf("InScope(\"\") err = %v, want ErrBadScope", err)
	}
}

// WithinCreated scopes a Trail to the row it creates next: that
// create's change carries "kind:<its id>", and so does every change
// after it. Until that create lands, any other tracked write is
// refused before it reaches SQLite, and the Trail keeps waiting.
func TestWithinCreatedScopesToTheRowItCreates(t *testing.T) {
	t.Parallel()
	g := open(t)
	other := probe{Name: "earlier"}
	seed := changes.Begin("member:1").Because("ProbeRecorded", "")
	if err := g.WithContext(seed.Context(inRequest("req-0"))).Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	tr := changes.Begin("member:1").WithinCreated("session")
	ctx := tr.Context(inRequest("req-c"))
	tr.Because("ProbeRenamed", "")
	if err := g.WithContext(ctx).Model(&other).Select("Name").Updates(map[string]any{"Name": "moved"}).Error; !errors.Is(err, changes.ErrScopePending) {
		t.Fatalf("update before the create: err = %v, want ErrScopePending", err)
	}
	tr.Because("ProbeRecorded", "")
	if err := g.WithContext(ctx).Create(&[]probe{{Name: "a"}, {Name: "b"}}).Error; !errors.Is(err, changes.ErrScopePending) {
		t.Fatalf("two-row create: err = %v, want ErrScopePending", err)
	}
	if tr.Scope() != "" {
		t.Fatalf("Scope() before the create = %q", tr.Scope())
	}
	p := probe{Name: "new"}
	if err := g.WithContext(ctx).Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	want := "session:" + strconv.FormatInt(p.ID, 10)
	tr.Because("ProbeRenamed", "")
	if err := g.WithContext(ctx).Model(&p).Select("Name").Updates(map[string]any{"Name": "renamed"}).Error; err != nil {
		t.Fatal(err)
	}
	got := tr.Recorded()
	if len(got) != 2 || got[0].EntityID != p.ID || got[0].Scope != want || got[1].Scope != want || tr.Scope() != want {
		t.Fatalf("changes %+v, Scope() %q, want both in %s", got, tr.Scope(), want)
	}
	var n int64
	g.Model(&probe{}).Count(&n)
	if n != 2 {
		t.Fatalf("probes = %d, want the seed and the scoped one: a refused write landed", n)
	}

	cases := []struct {
		name string
		tr   *changes.Trail
		want error
	}{
		{"bad kind", changes.Begin("member:1").WithinCreated("Session"), changes.ErrBadScope},
		{"kind with id", changes.Begin("member:1").WithinCreated("session:1"), changes.ErrBadScope},
		{"already scoped", changes.Begin("member:1").Within("session:1").WithinCreated("session"), changes.ErrScopeConflict},
		{"twice", changes.Begin("member:1").WithinCreated("session").WithinCreated("session"), changes.ErrScopeConflict},
		{"Within while waiting", changes.Begin("member:1").WithinCreated("session").Within("session:1"), changes.ErrScopeConflict},
	}
	for _, c := range cases {
		c.tr.Because("ProbeRecorded", "")
		if err := g.WithContext(c.tr.Context(inRequest("req-x"))).Create(&probe{Name: c.name}).Error; !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

// A scope may refuse writes (Policy.Admit): a frozen session takes
// none but its reopening. Admit is asked on the statement's own
// connection, inside the write's transaction, with the scope and the
// reason, before the write reaches SQLite; a refusal lands neither the
// row nor its change and is ErrNotAdmitted wrapping Admit's error. An
// unscoped Trail, and one waiting on WithinCreated, is never asked.
func TestAScopeThatDoesNotAdmitAWriteRefusesIt(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &probe{}); err != nil {
		t.Fatal(err)
	}
	closed := errors.New("probe 1 is closed")
	var asked []string
	admit := func(tx *gorm.DB, scope, reason string) error {
		asked = append(asked, scope+" "+reason)
		var n int64 // a read on the write's own connection
		if err := tx.Table("probes").Where("name = ?", "closed").Count(&n).Error; err != nil {
			return err
		}
		if scope == "probe:1" && n > 0 && reason != "ProbeReopened" {
			return closed
		}
		return nil
	}
	if err := changes.Install(d.G, changes.Policy{Tracked: []string{"probes"}, Correlation: correlation, Admit: admit}); err != nil {
		t.Fatal(err)
	}
	g := d.G
	write := func(scope, reason, name string) error {
		tr := changes.Begin("member:1").Because(reason, "")
		if scope != "" {
			tr.Within(scope)
		}
		return g.WithContext(tr.Context(inRequest("req-" + name))).Transaction(func(tx *gorm.DB) error {
			return tx.Create(&probe{Name: name}).Error
		})
	}
	if err := write("probe:1", "ProbeClosed", "closed"); err != nil {
		t.Fatal(err)
	}
	err = write("probe:1", "ProbeRecorded", "refused")
	if !errors.Is(err, changes.ErrNotAdmitted) || !errors.Is(err, closed) {
		t.Fatalf("err = %v, want ErrNotAdmitted wrapping Admit's", err)
	}
	for _, ok := range [][3]string{{"probe:1", "ProbeReopened", "reopened"}, {"probe:2", "ProbeRecorded", "other"}, {"", "ProbeRecorded", "unscoped"}} {
		if err := write(ok[0], ok[1], ok[2]); err != nil {
			t.Fatalf("%v: %v", ok, err)
		}
	}
	created := changes.Begin("member:1").Because("ProbeRecorded", "").WithinCreated("probe")
	if err := g.WithContext(created.Context(inRequest("req-c"))).Create(&probe{Name: "created"}).Error; err != nil {
		t.Fatal(err)
	}
	var names []string
	g.Model(&probe{}).Order("id").Pluck("name", &names)
	if strings.Join(names, ",") != "closed,reopened,other,unscoped,created" {
		t.Fatalf("probes = %v: the refused write landed, or an admitted one did not", names)
	}
	var n int64
	g.Model(&changes.Change{}).Count(&n)
	if n != 5 {
		t.Fatalf("changes = %d, want 5", n)
	}
	want := "probe:1 ProbeClosed,probe:1 ProbeRecorded,probe:1 ProbeReopened,probe:2 ProbeRecorded"
	if strings.Join(asked, ",") != want {
		t.Fatalf("Admit asked %v, want %s", asked, want)
	}
}

// A Trail may keep a ledger beside the record (OnRecord): told each
// change as it lands, on the write's own connection, so what it writes
// commits with the change or rolls back with it, and its refusal
// refuses the write.
func TestOnRecordWritesWithTheChangeOrNotAtAll(t *testing.T) {
	t.Parallel()
	g := open(t)
	if err := g.Exec("CREATE TABLE ledger (change_id INTEGER NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	note := func(tx *gorm.DB, c changes.Change) error {
		return tx.Exec("INSERT INTO ledger (change_id) VALUES (?)", c.ID).Error
	}
	write := func(name string, fail error, fns ...func(*gorm.DB, changes.Change) error) error {
		tr := changes.Begin("member:1").Because("ProbeRecorded", "")
		for _, fn := range fns {
			tr.OnRecord(fn)
		}
		return g.WithContext(tr.Context(inRequest("req-" + name))).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&[]probe{{Name: name}, {Name: name + "2"}}).Error; err != nil {
				return err
			}
			return fail
		})
	}
	if err := write("kept", nil, note); err != nil {
		t.Fatal(err)
	}
	if err := write("rolled", errors.New("the handler refused"), note); err == nil {
		t.Fatal("the failing transaction committed")
	}
	refused := errors.New("the ledger refuses")
	if err := write("refused", nil, note, func(*gorm.DB, changes.Change) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the ledger's refusal", err)
	}
	var ids, ledger []int64
	g.Model(&changes.Change{}).Order("id").Pluck("id", &ids)
	g.Table("ledger").Order("change_id").Pluck("change_id", &ledger)
	if len(ids) != 2 || !slices.Equal(ids, ledger) {
		t.Fatalf("changes %v, ledger %v: want the kept batch's two in both", ids, ledger)
	}
}

// The allow-list: a reason writes only the tables and columns it
// declares. A wrong reason is refused before SQLite, with the reason,
// the table and the column named.
func TestAWriteItsReasonDoesNotDeclareIsRefused(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	g := d.G
	if err := g.AutoMigrate(&changes.Change{}, &probe{}); err != nil {
		t.Fatal(err)
	}
	allow := map[string]changes.Writes{
		"ProbeRecorded": {"probes": {"id", "name", "cents"}},
		"ProbeRenamed":  {"probes": {"name"}},
		"OtherRecorded": {"others": {"id"}},
	}
	if err := changes.Install(g, changes.Policy{Tracked: []string{"probes"}, Correlation: correlation, Allow: allow}); err != nil {
		t.Fatal(err)
	}
	write := func(reason string, f func(*gorm.DB) error) error {
		return f(g.WithContext(changes.Begin("human").Because(reason, "").Context(inRequest("req-1"))))
	}
	p := probe{Name: "x", Cents: 1}
	if err := write("ProbeRecorded", func(g *gorm.DB) error { return g.Create(&p).Error }); err != nil {
		t.Fatalf("declared create: %v", err)
	}
	if err := write("ProbeRenamed", func(g *gorm.DB) error { return g.Model(&p).Update("name", "y").Error }); err != nil {
		t.Fatalf("declared update: %v", err)
	}
	for _, c := range []struct {
		reason, want string
		f            func(*gorm.DB) error
	}{
		{"OtherRecorded", "OtherRecorded does not write probes (it writes others)", func(g *gorm.DB) error { return g.Create(&probe{Name: "z"}).Error }},
		{"ProbeRenamed", "ProbeRenamed does not write probes.cents", func(g *gorm.DB) error { return g.Model(&p).Update("cents", 9).Error }},
		{"ProbeRenamed", "ProbeRenamed does not write probes.cents", func(g *gorm.DB) error { return g.Create(&probe{Name: "z"}).Error }},
		{"Undeclared", "Undeclared does not write probes", func(g *gorm.DB) error { return g.Delete(&p).Error }},
	} {
		err := write(c.reason, c.f)
		if !errors.Is(err, changes.ErrReasonMismatch) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want ErrReasonMismatch naming %q", c.reason, err, c.want)
		}
	}
	var n, m int64
	g.Model(&probe{}).Count(&n)
	g.Model(&changes.Change{}).Count(&m)
	if n != 1 || m != 2 {
		t.Fatalf("a refused write landed: %d probes, %d changes; want 1 and 2", n, m)
	}
}

// tagged is a model whose JSON is an API: its diff's keys are the JSON
// tags, not the Go names, and the allow-list still reads them as the
// columns they are.
type tagged struct {
	ID         int64  `json:"id"`
	ElementKey string `json:"esId"`
	Verdict    string `json:"verdict,omitempty"`
}

func TestTheAllowListReadsAJSONTaggedDiffAsItsColumns(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	g := d.G
	if err := g.AutoMigrate(&changes.Change{}, &tagged{}); err != nil {
		t.Fatal(err)
	}
	allow := map[string]changes.Writes{
		"FlagRaised":  {"taggeds": {"id", "element_key", "verdict"}},
		"FlagRenamed": {"taggeds": {"verdict"}},
	}
	if err := changes.Install(g, changes.Policy{Tracked: []string{"taggeds"}, Correlation: correlation, Allow: allow}); err != nil {
		t.Fatal(err)
	}
	write := func(reason string, f func(*gorm.DB) error) error {
		return f(g.WithContext(changes.Begin("human").Because(reason, "").Context(inRequest("req-1"))))
	}
	row := tagged{ElementKey: "es-1", Verdict: "open"}
	if err := write("FlagRaised", func(g *gorm.DB) error { return g.Create(&row).Error }); err != nil {
		t.Fatalf("a create whose diff says esId, declared as element_key: %v", err)
	}
	err = write("FlagRenamed", func(g *gorm.DB) error { return g.Model(&row).Updates(tagged{ElementKey: "es-2"}).Error })
	if !errors.Is(err, changes.ErrReasonMismatch) || !strings.Contains(err.Error(), "FlagRenamed does not write taggeds.element_key") {
		t.Fatalf("an update of esId under a reason without element_key: err = %v, want ErrReasonMismatch naming taggeds.element_key", err)
	}
}

// The boot census: a table nobody declared stops Install, named, and
// declaring it (untracked, with the reason) lets the same database
// boot. Plumbing (changes) and SQLite's own tables need no line.
func TestInstallRefusesAnUndeclaredTableByName(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &probe{}); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE TABLE planted_inbox (id INTEGER PRIMARY KEY AUTOINCREMENT)",
		"CREATE TABLE planted_ledger (id INTEGER PRIMARY KEY)",
	} {
		if err := d.G.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := changes.Policy{Tracked: []string{"probes"}, Correlation: correlation}
	err = changes.Install(d.G, p)
	if !errors.Is(err, changes.ErrUndeclaredTable) || !strings.Contains(err.Error(), "planted_inbox, planted_ledger") {
		t.Fatalf("Install with two undeclared tables: err = %v, want ErrUndeclaredTable naming both", err)
	}
	if strings.Contains(err.Error(), "sqlite_sequence") || strings.Contains(err.Error(), "probes") {
		t.Fatalf("the census named a declared or internal table: %v", err)
	}
	p.Untracked = map[string]string{"planted_inbox": "an inbox", "planted_ledger": "a tick ledger"}
	if err := changes.Install(d.G, p); err != nil {
		t.Fatalf("Install with both declared untracked: %v", err)
	}
}

// note is a table an invariant shapes, named as the welcome policy's.
type note struct {
	ID    int64
	Title string
}

// openShaped is a database whose policy declares the welcome
// invariant: EveryNewMemberIsWelcomed shapes WelcomeNoteWritten on
// notes, and ACorrectionChangesSomething guards NoteCorrected but
// shapes nothing.
func openShaped(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &note{}); err != nil {
		t.Fatal(err)
	}
	if err := changes.Install(d.G, shapedPolicy()); err != nil {
		t.Fatal(err)
	}
	return d.G
}

func shapedPolicy() changes.Policy {
	return changes.Policy{
		Tracked:     []string{"notes"},
		Correlation: correlation,
		Allow: map[string]changes.Writes{
			"WelcomeNoteWritten": {"notes": {"id", "title"}},
			"NoteWritten":        {"notes": {"id", "title"}},
			"NoteCorrected":      {"notes": {"title"}},
		},
		Invariants: []changes.Invariant{
			{Name: "EveryNewMemberIsWelcomed", Context: "notes", Rule: "Every member who joins gets a welcome note.",
				Shapes: map[string][]string{"WelcomeNoteWritten": {"notes"}}},
			{Name: "ACorrectionChangesSomething", Context: "notes", Rule: "A correction must change the note's title or its body.",
				Guards: []string{"NoteCorrected"}},
		},
	}
}

// landed is how many notes and changes the database holds.
func landed(t *testing.T, g *gorm.DB) (notes, records int64) {
	t.Helper()
	g.Model(&note{}).Count(&notes)
	g.Model(&changes.Change{}).Count(&records)
	return notes, records
}

// Scenario "rule required": a write a declared rule shapes, made
// without naming it, is refused before SQLite, naming the reason, the
// table and the rule, and nothing lands.
func TestAShapedWriteWithoutItsRuleIsRefused(t *testing.T) {
	t.Parallel()
	g := openShaped(t)
	tr := changes.Begin("agent:welcome").Because("WelcomeNoteWritten", "")
	err := g.WithContext(tr.Context(inRequest("req-1"))).Create(&note{Title: "Welcome"}).Error
	if !errors.Is(err, changes.ErrRuleRequired) {
		t.Fatalf("err = %v, want ErrRuleRequired", err)
	}
	for _, name := range []string{"WelcomeNoteWritten", "notes", "EveryNewMemberIsWelcomed"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("err = %q, want it to name %q", err, name)
		}
	}
	if n, m := landed(t, g); n != 0 || m != 0 {
		t.Fatalf("a refused write landed: %d notes, %d changes; want 0 and 0", n, m)
	}
}

// Scenario "rule recorded": the write that names its rule with By
// lands, and its change carries the rule, in the Trail and the table.
func TestAShapedWriteRecordsItsRule(t *testing.T) {
	t.Parallel()
	g := openShaped(t)
	tr := changes.Begin("agent:welcome").Because("WelcomeNoteWritten", "").By("EveryNewMemberIsWelcomed")
	if err := g.WithContext(tr.Context(inRequest("req-1"))).Create(&note{Title: "Welcome"}).Error; err != nil {
		t.Fatalf("a shaped write naming its rule: %v", err)
	}
	if c, _ := tr.Last(); c.Rule != "EveryNewMemberIsWelcomed" {
		t.Fatalf("recorded rule = %q, want EveryNewMemberIsWelcomed", c.Rule)
	}
	var rule string
	g.Raw("SELECT rule FROM changes").Scan(&rule)
	if rule != "EveryNewMemberIsWelcomed" {
		t.Fatalf("changes.rule = %q, want EveryNewMemberIsWelcomed", rule)
	}
}

// A named rule that does not shape the write is refused, naming the
// reason, the table and the rule: a declared rule that only guards,
// a rule on a reason it does not shape, and a rule nobody declared.
func TestAWriteNamingAnotherRuleIsRefused(t *testing.T) {
	t.Parallel()
	g := openShaped(t)
	for _, c := range []struct{ reason, rule string }{
		{"WelcomeNoteWritten", "ACorrectionChangesSomething"},
		{"NoteWritten", "EveryNewMemberIsWelcomed"},
		{"WelcomeNoteWritten", "NoSuchRule"},
	} {
		tr := changes.Begin("agent:welcome").Because(c.reason, "").By(c.rule)
		err := g.WithContext(tr.Context(inRequest("req-1"))).Create(&note{Title: "Welcome"}).Error
		if !errors.Is(err, changes.ErrRuleMismatch) {
			t.Errorf("%s by %s: err = %v, want ErrRuleMismatch", c.reason, c.rule, err)
			continue
		}
		for _, name := range []string{c.reason, "notes", c.rule} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("%s by %s: err = %q, want it to name %q", c.reason, c.rule, err, name)
			}
		}
	}
	if n, m := landed(t, g); n != 0 || m != 0 {
		t.Fatalf("a refused write landed: %d notes, %d changes; want 0 and 0", n, m)
	}
}

// By names the rule for the writes under the current Because only: a
// new Because clears it, so a second shaped write must name it again,
// and an unshaped write after it records no rule (NULL, not "").
func TestANewBecauseClearsTheRule(t *testing.T) {
	t.Parallel()
	g := openShaped(t)
	tr := changes.Begin("agent:welcome").Because("WelcomeNoteWritten", "").By("EveryNewMemberIsWelcomed")
	w := g.WithContext(tr.Context(inRequest("req-1")))
	if err := w.Create(&note{Title: "Welcome"}).Error; err != nil {
		t.Fatalf("the first shaped write: %v", err)
	}
	tr.Because("WelcomeNoteWritten", "")
	if err := w.Create(&note{Title: "Again"}).Error; !errors.Is(err, changes.ErrRuleRequired) {
		t.Fatalf("a shaped write after a new Because: err = %v, want ErrRuleRequired", err)
	}
	tr.Because("NoteWritten", "")
	if err := w.Create(&note{Title: "Plain"}).Error; err != nil {
		t.Fatalf("an unshaped write after a new Because: %v", err)
	}
	if c, _ := tr.Last(); c.Rule != "" {
		t.Fatalf("the unshaped write recorded rule %q, want none", c.Rule)
	}
	var null int64
	g.Raw("SELECT count(*) FROM changes WHERE rule IS NULL").Scan(&null)
	if null != 1 {
		t.Fatalf("%d changes with a NULL rule, want the unshaped one", null)
	}
}

// A shaped table must be one its reason writes: Install refuses an
// invariant whose Shapes name a (reason, table) Allow does not, each
// one named with its rule, reason and table, in a sorted list; a
// policy whose shapes are all written installs.
func TestInstallRefusesAShapeItsReasonDoesNotWrite(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &note{}); err != nil {
		t.Fatal(err)
	}
	p := shapedPolicy()
	p.Invariants = append(p.Invariants, changes.Invariant{Name: "EveryCorrectionIsFiled", Context: "notes",
		Rule: "Every correction is filed.", Shapes: map[string][]string{"NoteCorrected": {"files"}, "AFileFiled": {"notes"}}})
	err = changes.Install(d.G, p)
	if !errors.Is(err, changes.ErrBadInvariant) {
		t.Fatalf("Install with two shapes no reason writes: err = %v, want ErrBadInvariant", err)
	}
	want := "EveryCorrectionIsFiled shapes AFileFiled on notes; EveryCorrectionIsFiled shapes NoteCorrected on files"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to name both shapes, sorted: %q", err, want)
	}
	if err := changes.Install(d.G, shapedPolicy()); err != nil {
		t.Fatalf("Install with every shape written: %v", err)
	}
}

// openLedgered is a database built the way an app's is, by
// migrate.Apply over changes.Schema, so rastrillo's ledger holds the
// row changes/0005_changes_rule; its applied_at is then set to
// cutoff, so records can be placed on either side of it. It has no
// Install: the records Verify is to judge are ones the hook would
// refuse, written straight into the tables.
func openLedgered(t *testing.T, cutoff string) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := migrate.Apply(context.Background(), d, migrate.Merge(changes.Schema)); err != nil {
		t.Fatal(err)
	}
	if err := d.G.AutoMigrate(&note{}); err != nil {
		t.Fatal(err)
	}
	res := d.G.Exec("UPDATE rastrillo_migrations SET applied_at = ? WHERE id = 'changes/0005_changes_rule'", cutoff)
	if res.Error != nil || res.RowsAffected != 1 {
		t.Fatalf("setting the 0005 ledger row's applied_at: err %v, %d rows", res.Error, res.RowsAffected)
	}
	return d.G
}

// recorded writes a note and the change that creates it, at
// createdAt, with rule ("" is NULL), past every check the hook makes.
func recorded(t *testing.T, g *gorm.DB, id int64, reason, rule, createdAt string) {
	t.Helper()
	var r any
	if rule != "" {
		r = rule
	}
	if err := g.Exec("INSERT INTO notes (id, title) VALUES (?, 'Welcome')", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.Exec("INSERT INTO changes (reason, cause, request_id, correlation_id, actor, entity, entity_id, op, diff, rule, created_at) "+
		"VALUES (?, 'request:r', 'request:r', 'r', 'agent:welcome', 'notes', ?, 'create', ?, ?, ?)",
		reason, id, `{"ID":`+strconv.FormatInt(id, 10)+`,"Title":"Welcome"}`, r, createdAt).Error; err != nil {
		t.Fatal(err)
	}
}

// Scenarios "old record not held", "new record held" and "unknown
// rule" (RUS-83 D11): Verify holds a shaped (reason, table) record to
// name its rule only when it was created after the 0005 migration
// added the column, and a record naming a rule no invariant declares
// at any age. A record that names its rule, and one no rule shapes,
// say nothing.
func TestVerifyHoldsRecordsToTheirRulesFromTheRuleColumnOn(t *testing.T) {
	t.Parallel()
	g := openLedgered(t, "2026-10-01T00:00:00Z")
	recorded(t, g, 1, "WelcomeNoteWritten", "", "2026-09-30 12:00:00+00:00")
	recorded(t, g, 2, "WelcomeNoteWritten", "", "2026-10-02 12:00:00+00:00")
	recorded(t, g, 3, "NoteWritten", "NoSuchRule", "2026-09-30 12:00:00+00:00")
	recorded(t, g, 4, "WelcomeNoteWritten", "EveryNewMemberIsWelcomed", "2026-10-02 12:00:00+00:00")
	recorded(t, g, 5, "NoteWritten", "", "2026-10-02 12:00:00+00:00")
	ms, err := changes.Verify(g, []string{"notes"}, nil, shapedPolicy().Invariants)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(ms))
	for i, m := range ms {
		got[i] = m.String()
	}
	want := []string{
		"notes #2: change #2 is WelcomeNoteWritten, which EveryNewMemberIsWelcomed shapes, and names no rule",
		"notes #3: change #3 names rule NoSuchRule, which no invariant declares",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Verify = %q,\nwant %q", got, want)
	}
}

// With the rule column and no ledger (a database AutoMigrate built),
// Verify holds every shaped record to its rule, however old; with a
// ledger that has no 0005 row, every record predates the rule and
// none is held; a ledger applied_at it cannot read is an error, not a
// silent cutoff.
func TestVerifyWithoutALedgerHoldsEveryRecord(t *testing.T) {
	t.Parallel()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &note{}); err != nil {
		t.Fatal(err)
	}
	recorded(t, d.G, 1, "WelcomeNoteWritten", "", "2001-01-01 00:00:00+00:00")
	ms, err := changes.Verify(d.G, []string{"notes"}, nil, shapedPolicy().Invariants)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].EntityID != 1 || !strings.Contains(ms[0].Problem, "names no rule") {
		t.Fatalf("Verify = %v, want the one record that names no rule", ms)
	}

	g := openLedgered(t, "2026-10-01T00:00:00Z")
	if err := g.Exec("DELETE FROM rastrillo_migrations WHERE id = ?", changes.RuleMigration).Error; err != nil {
		t.Fatal(err)
	}
	recorded(t, g, 1, "WelcomeNoteWritten", "", "2001-01-01 00:00:00+00:00")
	if ms, err := changes.Verify(g, []string{"notes"}, nil, shapedPolicy().Invariants); err != nil || len(ms) != 0 {
		t.Fatalf("a ledger without the 0005 row: Verify = %v, %v; want no mismatch, every record predates the rule", ms, err)
	}

	g = openLedgered(t, "not a time")
	if _, err := changes.Verify(g, []string{"notes"}, nil, nil); err == nil || !strings.Contains(err.Error(), changes.RuleMigration) {
		t.Fatalf("an unreadable applied_at: err = %v, want one naming %s", err, changes.RuleMigration)
	}
}

// A tenant still at migration 0004 (no 0005 ledger row, no rule
// column), verified from a copy that was never migrated: every record
// predates the rule column, so none is held to name one, and the
// unknown-rule check, with no column to read, reports nothing and
// does not fail.
func TestVerifyBeforeTheRuleMigrationHoldsNoRecord(t *testing.T) {
	t.Parallel()
	g := openLedgered(t, "2026-10-01T00:00:00Z")
	for _, stmt := range []string{
		"DELETE FROM rastrillo_migrations WHERE id = 'changes/0005_changes_rule'",
		"ALTER TABLE changes DROP COLUMN rule",
		"INSERT INTO notes (id, title) VALUES (1, 'Welcome')",
		"INSERT INTO changes (reason, cause, request_id, correlation_id, actor, entity, entity_id, op, diff, created_at) " +
			`VALUES ('WelcomeNoteWritten', 'request:r', 'request:r', 'r', 'agent:welcome', 'notes', 1, 'create', '{"ID":1,"Title":"Welcome"}', '2026-09-01 12:00:00+00:00')`,
	} {
		if err := g.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	ms, err := changes.Verify(g, []string{"notes"}, nil, shapedPolicy().Invariants)
	if err != nil {
		t.Fatalf("Verify on a database without the rule column: %v", err)
	}
	if len(ms) != 0 {
		t.Fatalf("Verify = %v, want no mismatch: the record predates the rule column", ms)
	}

	// Without the column and without a ledger, too: the column's
	// absence alone says no record can name a rule.
	if err := g.Exec("DROP TABLE rastrillo_migrations").Error; err != nil {
		t.Fatal(err)
	}
	if ms, err := changes.Verify(g, []string{"notes"}, nil, shapedPolicy().Invariants); err != nil || len(ms) != 0 {
		t.Fatalf("no rule column and no ledger: Verify = %v, %v; want no mismatch", ms, err)
	}
}

// jsonLines is a logger whose lines a test reads back, one map each.
func jsonLines() (*slog.Logger, func(t *testing.T) []map[string]any) {
	var buf strings.Builder
	return slog.New(slog.NewJSONHandler(&buf, nil)), func(t *testing.T) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("log line %q: %v", line, err)
			}
			out = append(out, m)
		}
		return out
	}
}

// A refusal names a declared rule: it lands nothing, comes back with
// the rule, its context and the detail, and logs exactly one Info
// line "refused" naming the rule, the context, the entry point and
// the actor.
func TestARefusalLandsNothingAndLogsOneLine(t *testing.T) {
	t.Parallel()
	g := openShaped(t)
	logger, lines := jsonLines()
	p := shapedPolicy()
	p.Logger = logger
	tr := p.Begin("member:3")
	tr.EntryPoint = "CorrectNote"
	ref := tr.Refuse(inRequest("req-1"), "ACorrectionChangesSomething", "That correction changes nothing.")
	if ref.Err != nil || ref.Rule != "ACorrectionChangesSomething" || ref.Context != "notes" || ref.Detail != "That correction changes nothing." {
		t.Fatalf("Refused = %+v, want the rule, context notes and the detail, no Err", *ref)
	}
	got := lines(t)
	if len(got) != 1 {
		t.Fatalf("lines = %v, want one", got)
	}
	for k, want := range map[string]string{"msg": "refused", "level": "INFO", "rule": "ACorrectionChangesSomething",
		"context": "notes", "entry_point": "CorrectNote", "actor": "member:3"} {
		if got[0][k] != want {
			t.Errorf("line %s = %v, want %q", k, got[0][k], want)
		}
	}
	if n, m := landed(t, g); n != 0 || m != 0 {
		t.Fatalf("a refusal landed %d notes, %d changes; want 0 and 0", n, m)
	}
}

// A rule no invariant declares is a programming error: under go test
// Refuse panics with ErrUndeclaredRule naming it, whether the name is
// unknown, empty, or the Trail came from Begin and declares nothing.
func TestARefusalByAnUndeclaredRulePanicsInTests(t *testing.T) {
	t.Parallel()
	logger, lines := jsonLines()
	p := shapedPolicy()
	p.Logger = logger
	nameless := shapedPolicy() // an invariant named "" (decisions D4) is still no rule to refuse by
	nameless.Logger = logger
	nameless.Invariants = append(nameless.Invariants, changes.Invariant{Context: "notes", Guards: []string{"NoteCorrected"}})
	for _, c := range []struct {
		name string
		tr   *changes.Trail
		rule string
	}{
		{"unknown", p.Begin("member:3"), "NoSuchRule"},
		{"empty", nameless.Begin("member:3"), ""},
		{"from Begin", changes.Begin("member:3"), "ACorrectionChangesSomething"},
	} {
		func() {
			defer func() {
				err, _ := recover().(error)
				if !errors.Is(err, changes.ErrUndeclaredRule) || !strings.Contains(err.Error(), `"`+c.rule+`"`) {
					t.Errorf("%s: recovered %v, want ErrUndeclaredRule naming %q", c.name, err, c.rule)
				}
			}()
			c.tr.Refuse(inRequest("req-1"), c.rule, "no")
		}()
	}
	if got := lines(t); len(got) != 0 {
		t.Fatalf("an undeclared refusal logged %v, want nothing", got)
	}
}

// Outside tests the same programming error does not panic the
// request: Refused.Err carries ErrUndeclaredRule and nothing is
// logged as refused.
func TestARefusalByAnUndeclaredRuleIsAnErrorInProduction(t *testing.T) {
	t.Parallel()
	logger, lines := jsonLines()
	p := shapedPolicy()
	p.Logger = logger
	ref := changes.Lenient(p.Begin("member:3")).Refuse(inRequest("req-1"), "NoSuchRule", "no")
	if !errors.Is(ref.Err, changes.ErrUndeclaredRule) || ref.Rule != "NoSuchRule" {
		t.Fatalf("Refused = %+v, want Err ErrUndeclaredRule for NoSuchRule", *ref)
	}
	if got := lines(t); len(got) != 0 {
		t.Fatalf("an undeclared refusal logged %v, want nothing", got)
	}
}

// A Policy with no Logger logs its refusals to slog.Default().
func TestARefusalWithoutALoggerUsesTheDefault(t *testing.T) {
	t.Parallel()
	if got := changes.LoggerOf(shapedPolicy().Begin("member:3")); got != slog.Default() {
		t.Fatalf("logger = %p, want slog.Default() %p", got, slog.Default())
	}
}
