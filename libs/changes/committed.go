package changes

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"gorm.io/gorm"
)

// Policy.Committed is the change hook's after-commit half: what lets a
// doorbell ring (ADR 0004) for changes that landed, and never for ones
// a rollback took back. GORM has no commit callback, so Install wraps
// the handle's pool: a transaction begun through it is a txPool, which
// collects the scope of every change record (record) and hands them on
// after its Commit succeeds. A Rollback drops them.
//
// What it sees: every tracked write through the *gorm.DB Install was
// given, inside an explicit Transaction (begun on the wrapped pool) or
// outside one (GORM's own per-statement transaction, which committed
// swaps onto the wrapped pool, since dbresolver hands a write the bare
// pool it captured at Use). A statement with no transaction at all
// (SkipDefaultTransaction) is told right after its change rows land:
// that is its commit. What it does not see: a transaction begun on
// another handle (the bare *sql.DB), whose commit it cannot hear; such
// a write is never told, rather than told before it is safe.
//
// Over-telling is possible and harmless by design: a nested
// Transaction's savepoint rolled back inside a committed outer one
// leaves its scopes collected. A listener re-reads what it needs from
// the table, so a spurious call finds nothing new.

// pool is the handle's write pool, wrapped so its transactions can be
// heard committing.
type pool struct {
	gorm.ConnPool
	begin gorm.TxBeginner
	told  func([]string)
}

// BeginTx is gorm.ConnPoolBeginner: Begin goes through here.
func (p *pool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.begin.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &txPool{Tx: tx, told: p.told}, nil
}

// GetDBConn is gorm.GetDBConnector, so (*gorm.DB).DB() still answers
// the *sql.DB underneath.
func (p *pool) GetDBConn() (*sql.DB, error) {
	if d, ok := p.ConnPool.(*sql.DB); ok {
		return d, nil
	}
	if c, ok := p.ConnPool.(gorm.GetDBConnector); ok {
		return c.GetDBConn()
	}
	return nil, gorm.ErrInvalidDB
}

// txPool is one transaction begun through pool: a *sql.Tx (ConnPool and
// TxCommitter both) that remembers the scopes its changes landed in.
type txPool struct {
	*sql.Tx
	told   func([]string)
	scopes map[string]bool
}

// Commit commits, then tells the scopes, once each, sorted.
func (t *txPool) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if len(t.scopes) > 0 {
		out := make([]string, 0, len(t.scopes))
		for s := range t.scopes {
			out = append(out, s)
		}
		sort.Strings(out)
		t.scopes = nil
		t.told(out)
	}
	return nil
}

// Rollback forgets what it collected.
func (t *txPool) Rollback() error {
	t.scopes = nil
	return t.Tx.Rollback()
}

// installCommitted wraps g's pool and swaps it back onto every write
// statement dbresolver pointed at the bare one, before GORM begins the
// statement's own transaction.
func installCommitted(g *gorm.DB, told func([]string)) error {
	inner := g.Config.ConnPool
	begin, ok := inner.(gorm.TxBeginner)
	if !ok {
		return errors.New("changes: Policy.Committed needs a pool that begins transactions (*sql.DB)")
	}
	p := &pool{ConnPool: inner, begin: begin, told: told}
	g.Config.ConnPool = p
	g.Statement.ConnPool = p
	swap := func(db *gorm.DB) {
		if db.Statement.ConnPool == inner {
			db.Statement.ConnPool = p
		}
	}
	c, u, d := g.Callback().Create(), g.Callback().Update(), g.Callback().Delete()
	for _, step := range []func() error{
		func() error { return c.Before("gorm:begin_transaction").Register("changes:pool_create", swap) },
		func() error { return u.Before("gorm:begin_transaction").Register("changes:pool_update", swap) },
		func() error { return d.Before("gorm:begin_transaction").Register("changes:pool_delete", swap) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// landed notes that a statement's changes landed in scope: on its
// transaction when it is one of ours, told now when it runs in none.
func landed(db *gorm.DB, scope string, told func([]string)) {
	if told == nil || scope == "" {
		return
	}
	switch cp := db.Statement.ConnPool.(type) {
	case *txPool:
		if cp.scopes == nil {
			cp.scopes = map[string]bool{}
		}
		cp.scopes[scope] = true
	case gorm.TxCommitter:
		// A transaction begun elsewhere: its commit is not ours to hear.
	default:
		told([]string{scope})
	}
}
