package changes_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"amadan.net/rastrillo/rastrillo/db"
	"gorm.io/gorm"

	"myproject/libs/changes"
)

// told is what Policy.Committed was told, and what a reader on another
// connection could already see at that moment: the proof that the
// call came after the commit, not inside the transaction.
type told struct {
	mu      sync.Mutex
	g       *gorm.DB
	calls   [][]string
	visible []int64
}

func (c *told) committed(scopes []string) {
	var n int64
	// The read pool: another connection, so only committed rows count.
	c.g.Model(&changes.Change{}).Where("scope IN ?", scopes).Count(&n)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, scopes)
	c.visible = append(c.visible, n)
}

func (c *told) got() ([][]string, []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls), slices.Clone(c.visible)
}

func openCommitted(t *testing.T) (*gorm.DB, *told) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "c.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.G.AutoMigrate(&changes.Change{}, &probe{}); err != nil {
		t.Fatal(err)
	}
	c := &told{g: d.G}
	if err := changes.Install(d.G, changes.Policy{Tracked: []string{"probes"}, Correlation: correlation, Committed: c.committed}); err != nil {
		t.Fatal(err)
	}
	return d.G, c
}

// A transaction that landed scoped changes tells Committed which
// scopes, once, after it commits: a reader elsewhere already sees them.
func TestCommittedHearsTheScopesAfterTheCommit(t *testing.T) {
	t.Parallel()
	g, c := openCommitted(t)
	tr := changes.Begin("member:1").Within("session:1").Because("ProbeRecorded", "")
	err := g.WithContext(tr.Context(inRequest("req-1"))).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&probe{Name: "a"}).Error; err != nil {
			return err
		}
		if calls, _ := c.got(); len(calls) != 0 {
			t.Errorf("told inside the transaction: %v", calls)
		}
		return tx.Create(&probe{Name: "b"}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, visible := c.got()
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"session:1"}) {
		t.Fatalf("Committed calls = %v, want one, [session:1]", calls)
	}
	if visible[0] != 2 {
		t.Fatalf("a reader saw %d changes when told, want both: told before the commit", visible[0])
	}
}

// A rolled-back transaction tells nothing.
func TestCommittedHearsNothingOfARollback(t *testing.T) {
	t.Parallel()
	g, c := openCommitted(t)
	tr := changes.Begin("member:1").Within("session:1").Because("ProbeRecorded", "")
	boom := errors.New("refused after the write")
	err := g.WithContext(tr.Context(inRequest("req-1"))).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&probe{Name: "a"}).Error; err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if calls, _ := c.got(); len(calls) != 0 {
		t.Fatalf("a rollback was told: %v", calls)
	}
}

// A write outside an explicit transaction (GORM's own per-statement
// one) is told too, after it commits; an unscoped one is not.
func TestCommittedHearsAWriteOutsideATransaction(t *testing.T) {
	t.Parallel()
	g, c := openCommitted(t)
	plain := changes.Begin("member:1").Because("ProbeRecorded", "")
	if err := g.WithContext(plain.Context(inRequest("req-1"))).Create(&probe{Name: "unscoped"}).Error; err != nil {
		t.Fatal(err)
	}
	if calls, _ := c.got(); len(calls) != 0 {
		t.Fatalf("an unscoped write was told: %v", calls)
	}
	tr := changes.Begin("member:1").Within("session:7").Because("ProbeRecorded", "")
	if err := g.WithContext(tr.Context(inRequest("req-2"))).Create(&probe{Name: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	calls, visible := c.got()
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"session:7"}) || visible[0] != 1 {
		t.Fatalf("Committed calls = %v (visible %v), want [session:7] seen", calls, visible)
	}
}

// One transaction across two scopes names both, sorted; the pool
// still answers DB() (rastrillo's sessions store takes the *sql.DB).
func TestCommittedNamesEveryScopeOnce(t *testing.T) {
	t.Parallel()
	g, c := openCommitted(t)
	ctx := inRequest("req-1")
	err := g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, s := range []string{"session:2", "session:1", "session:2"} {
			tr := changes.Begin("member:1").Within(s).Because("ProbeRecorded", "")
			if err := tx.WithContext(tr.Context(ctx)).Create(&probe{Name: s}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := c.got()
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"session:1", "session:2"}) {
		t.Fatalf("Committed calls = %v, want [[session:1 session:2]]", calls)
	}
	if sqlDB, err := g.DB(); err != nil || sqlDB == nil {
		t.Fatalf("DB() = %v, %v", sqlDB, err)
	}
	if err := sqlDBPing(g); err != nil {
		t.Fatal(err)
	}
}

func sqlDBPing(g *gorm.DB) error {
	s, err := g.DB()
	if err != nil {
		return err
	}
	return s.PingContext(context.Background())
}
