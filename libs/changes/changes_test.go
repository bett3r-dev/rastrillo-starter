package changes_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"
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
