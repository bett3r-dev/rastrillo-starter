// Package changes is the change-record convention: every mutation on
// a tracked table lands a Change row in the same transaction, or the
// write is refused. The reason on that row is the domain event
// vocabulary, in business language ("NoteWritten"), declared per
// bounded context in the app's reasons_<context>.go files. This is
// where domain events live: as a name on a diff, not as a type in
// code.
//
// A handler opens a Trail for the request (its actor), says why it is
// about to write with Because, and runs its GORM writes with the
// Trail's context. The GORM callbacks installed by Install do the
// rest: a tracked write with no reason in its context, or outside any
// request, is refused, so an unexplained change cannot land; an
// explained one is recorded with the field diff, the reason, its
// cause and the correlation of the request it ran in, which the
// callback reads off the write's own context (Policy.Correlation).
//
// The changes table is append-only: its migration (Schema, which an
// app merges into its BootSchema) adds SQLite triggers that abort any
// UPDATE or DELETE on it, which catches raw SQL as well as GORM. Enforcing the "must be explained" half at the
// database would need the request context, which a trigger cannot
// see, so that half is this package's callbacks.
package changes

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"amadan.net/rastrillo/rastrillo/migrate"
	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Schema creates the changes table, append-only by trigger. It is a
// subsystem's set, like sessions.Schema: merge it into the app's
// BootSchema, never its Schema, and keep Change out of the app's
// Models, or the migration generator would want to drop the table.
//
//	var BootSchema = migrate.Merge(sessions.Schema, changes.Schema, Schema)
var Schema = migrate.MustFromFS(migrationFS, "changes")

// Change is one explained mutation. Append-only: never updated, never
// deleted (the triggers in Schema refuse both).
type Change struct {
	ID int64
	// Reason is the business-language name of what happened, one of
	// the codes declared in the app's reasons*.go.
	Reason string `gorm:"index;not null"`
	// Cause is what led to this change: the id of an earlier Change
	// ("change:12"), or the root that started the chain when nothing
	// earlier did ("request:<trace id>", "bank:<provider id>",
	// "tick:<key>"). Chaining it is how "why did this row change?" is
	// answered from this table alone. A "change:" cause
	// must name a change that already exists, so a chain always
	// descends to a root and cannot loop.
	Cause string `gorm:"not null"`
	// RequestID is the root of the chain: the cause of the first
	// change in it. Every change answers "what started this?" in one
	// column, without walking.
	RequestID string `gorm:"index;not null"`
	// CorrelationID is the trace id of the request the write ran in:
	// the same id on every log line and span the request produced, and
	// on every request it caused or was caused by, since a traceparent
	// carries it across services. Read from the write's own context by
	// Policy.Correlation, never handed in.
	CorrelationID string `gorm:"index;not null"`
	// Actor is "member:<id>", "human" (signing up, before there is a
	// member) or "agent:<name>".
	Actor string `gorm:"not null"`
	// Entity is the table, EntityID the row.
	Entity   string `gorm:"index;not null"`
	EntityID int64  `gorm:"index;not null"`
	// Op is create, update or delete.
	Op string `gorm:"not null"`
	// Diff is the field diff as JSON: the whole row on create, the
	// written fields on update, the row as loaded on delete.
	Diff      string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
}

// ErrBadCause is the refusal of a "change:<id>" cause that names no
// existing change: a chain must descend to a root that already landed.
var ErrBadCause = errors.New("changes: cause names a change that does not exist")

// ErrUncorrelated is the refusal of a tracked write whose context
// carries no correlation: a write made outside any request's trace,
// which nothing could later tie back to what started it.
var ErrUncorrelated = errors.New("changes: write on a tracked table outside a request's trace")

// ErrUnexplained is the refusal: a tracked table was written with no
// Trail, or a Trail that has not said why.
var ErrUnexplained = errors.New("changes: write on a tracked table with no reason")

// Trail carries one request's explanation through its writes. Build
// it with Begin, put it in the context with Context, state each
// reason with Because before the writes it explains.
//
// It does not carry the correlation. That is read from the context
// each write runs under (Policy.Correlation), the way Node's
// AsyncLocalStorage hands a request's state to whatever runs on its
// behalf: Go's equivalent is the context.Context, passed explicitly,
// and every write already takes one.
type Trail struct {
	Actor string

	reason string
	cause  string // as given to Because; "" means the request itself
	// Resolved by require for the statement in flight, used by record.
	correlation string
	stmtCause   string
	requestID   string // the chain's root
	recorded    []Change
}

type ctxKey struct{}

// Begin opens a Trail for one inbound request or tick.
func Begin(actor string) *Trail {
	return &Trail{Actor: actor}
}

// Context returns ctx carrying t, for gorm's WithContext.
func (t *Trail) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// From returns the Trail in ctx, if any.
func From(ctx context.Context) (*Trail, bool) {
	t, ok := ctx.Value(ctxKey{}).(*Trail)
	return t, ok && t != nil
}

// Because sets the reason for the writes that follow. cause names
// what led to them; empty means the request itself, recorded as
// "request:<correlation>" once a write says which request that is.
func (t *Trail) Because(reason, cause string) *Trail {
	t.reason = reason
	t.cause = cause
	return t
}

// Recorded is every Change this Trail has landed, in order.
func (t *Trail) Recorded() []Change { return t.recorded }

// Last is the most recent Change landed, for chaining as a cause.
func (t *Trail) Last() (Change, bool) {
	if len(t.recorded) == 0 {
		return Change{}, false
	}
	return t.recorded[len(t.recorded)-1], true
}

// root resolves the chain's root for the current cause: the cause
// itself unless it names an earlier change, in which case that
// change's own root. The earlier change must exist, in this Trail
// or in the table; a cause that names none is refused.
func (t *Trail) root(db *gorm.DB, cause string) (string, error) {
	if !strings.HasPrefix(cause, "change:") {
		return cause, nil
	}
	for _, c := range t.recorded {
		if c.Ref() == cause {
			return c.RequestID, nil
		}
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(cause, "change:"), 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrBadCause, cause)
	}
	var earlier Change
	err = db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Select("request_id").First(&earlier, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("%w: %s (reason %s on %s)", ErrBadCause, cause, t.reason, db.Statement.Table)
	}
	if err != nil {
		return "", err
	}
	return earlier.RequestID, nil
}

// Ref is how a Change is named as another's cause.
func (c Change) Ref() string { return fmt.Sprintf("change:%d", c.ID) }

// Policy is what Install enforces: the tracked tables, and per table
// the columns an UPDATE may touch unexplained. The usual exemption is
// members.password_hash, written in a second step so the MemberJoined
// diff carries no secret.
type Policy struct {
	Tracked []string
	Exempt  map[string][]string
	// Correlation reads the id of the request a write runs in from the
	// write's context: in the app, the trace id. Required. A tracked
	// write whose context yields "" is refused (ErrUncorrelated).
	Correlation func(context.Context) string
}

// Install registers the callbacks on g for the policy's tracked
// tables and installs the database half of the same rule. Call once
// at boot, on the app's *gorm.DB. The changes table itself is never
// tracked.
//
// The database half: a TEMP trigger before every INSERT, UPDATE and
// DELETE on each tracked table aborts unless the hook has declared
// its intent in a TEMP table first (require writes it, record clears
// it). TEMP, because a persistent trigger cannot read a temp table
// and the intent cannot be persistent: it is one connection's state.
// The write pool is one connection (db.Open), so every write the app
// makes meets the trigger, including raw Exec and any future ORM; a
// migration runs before Install and another process's connection
// never sees the trigger, so those two paths are not covered, and
// the fold check (Verify) is what catches them after the fact.
func Install(g *gorm.DB, p Policy) error {
	if p.Correlation == nil {
		return errors.New("changes: Policy.Correlation is required")
	}
	set := map[string]bool{}
	for _, t := range p.Tracked {
		set[t] = true
	}
	if err := guard(g, p); err != nil {
		return err
	}
	cb := func(op string) func(*gorm.DB) {
		return func(db *gorm.DB) { record(db, set, op) }
	}
	req := func(op string) func(*gorm.DB) {
		return func(db *gorm.DB) { require(db, set, p, op) }
	}
	// require runs BEFORE the statement, so an unexplained write never
	// reaches SQLite: db.Open skips GORM's per-statement transaction,
	// so refusing afterwards would leave the row in place. record runs
	// after, when the primary key is known.
	c, u, d := g.Callback().Create(), g.Callback().Update(), g.Callback().Delete()
	steps := []func() error{
		func() error { return c.Before("gorm:create").Register("changes:require_create", req("create")) },
		func() error { return c.After("gorm:create").Register("changes:record_create", cb("create")) },
		func() error { return u.Before("gorm:update").Register("changes:require_update", req("update")) },
		func() error { return u.After("gorm:update").Register("changes:record_update", cb("update")) },
		func() error { return d.Before("gorm:delete").Register("changes:require_delete", req("delete")) },
		func() error { return d.After("gorm:delete").Register("changes:record_delete", cb("delete")) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

func require(db *gorm.DB, tracked map[string]bool, p Policy, op string) {
	st := db.Statement
	if db.Error != nil || st.Schema == nil || !tracked[st.Table] {
		return
	}
	t, ok := From(st.Context)
	if !ok || t.reason == "" {
		_ = db.AddError(fmt.Errorf("%w: %s %s", ErrUnexplained, op, st.Table))
		return
	}
	corr := p.Correlation(st.Context)
	if corr == "" {
		_ = db.AddError(fmt.Errorf("%w: %s %s", ErrUncorrelated, op, st.Table))
		return
	}
	cause := t.cause
	if cause == "" {
		cause = "request:" + corr
	}
	// The cause must already exist, before the write: db.Open runs no
	// per-statement transaction, so a refusal afterwards would leave
	// the row in place.
	root, err := t.root(db, cause)
	if err != nil {
		_ = db.AddError(err)
		return
	}
	t.correlation, t.stmtCause, t.requestID = corr, cause, root
	// Declare the intent the trigger will look for, on this
	// statement's connection. If the connection was replaced since
	// Install, its TEMP schema went with it: put the guard back.
	sess := db.Session(&gorm.Session{NewDB: true, SkipHooks: true})
	var n int64
	if err := sess.Raw("SELECT count(*) FROM sqlite_temp_master WHERE name = 'changes_intent'").Scan(&n).Error; err != nil {
		_ = db.AddError(err)
		return
	}
	if n == 0 {
		if err := guard(sess, p); err != nil {
			_ = db.AddError(err)
			return
		}
	}
	if err := sess.Exec("DELETE FROM changes_intent").Error; err != nil {
		_ = db.AddError(err)
		return
	}
	if err := sess.Exec("INSERT INTO changes_intent (entity) VALUES (?)", st.Table).Error; err != nil {
		_ = db.AddError(err)
	}
}

// guard installs the intent table and the triggers on the connection
// g's next statement runs on. Idempotent.
func guard(g *gorm.DB, p Policy) error {
	if err := g.Exec("CREATE TEMP TABLE IF NOT EXISTS changes_intent (entity TEXT NOT NULL)").Error; err != nil {
		return err
	}
	for _, t := range p.Tracked {
		of := ""
		if ex := p.Exempt[t]; len(ex) > 0 {
			cols, err := columns(g, t)
			if err != nil {
				return err
			}
			var keep []string
			for _, c := range cols {
				exempt := false
				for _, e := range ex {
					exempt = exempt || e == c
				}
				if !exempt {
					keep = append(keep, "`"+c+"`")
				}
			}
			of = " OF " + strings.Join(keep, ", ")
		}
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			when := op
			if op == "UPDATE" {
				when += of
			}
			stmt := fmt.Sprintf("CREATE TEMP TRIGGER IF NOT EXISTS changes_guard_%s_%s BEFORE %s ON main.`%s` BEGIN "+
				"SELECT RAISE(ABORT, 'changes: unexplained %s on %s') WHERE NOT EXISTS (SELECT 1 FROM changes_intent WHERE entity = '%s'); END",
				t, strings.ToLower(op), when, t, strings.ToLower(op), t, t)
			if err := g.Exec(stmt).Error; err != nil {
				return fmt.Errorf("changes: guard %s %s: %w", t, op, err)
			}
		}
	}
	return nil
}

func columns(g *gorm.DB, table string) ([]string, error) {
	rows, err := g.Raw("SELECT name FROM pragma_table_info(?)", table).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func record(db *gorm.DB, tracked map[string]bool, op string) {
	st := db.Statement
	if st.Schema == nil || !tracked[st.Table] {
		return
	}
	// The intent was for this one statement, whatever became of it.
	_ = db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Exec("DELETE FROM changes_intent").Error
	if db.Error != nil {
		return
	}
	if db.RowsAffected == 0 && op != "create" {
		return
	}
	t, ok := From(st.Context)
	if !ok || t.reason == "" {
		return // require already refused it
	}
	root := t.requestID
	rv := st.ReflectValue
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	var rows []reflect.Value
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			rows = append(rows, rv.Index(i))
		}
	default:
		rows = append(rows, rv)
	}
	for _, row := range rows {
		var id int64
		if pk := st.Schema.PrioritizedPrimaryField; pk != nil {
			if v, zero := pk.ValueOf(st.Context, row); !zero {
				if n, ok := v.(int64); ok {
					id = n
				}
			}
		}
		diff := diffOf(st.Dest, row, op)
		c := Change{
			Reason:        t.reason,
			Cause:         t.stmtCause,
			RequestID:     root,
			CorrelationID: t.correlation,
			Actor:         t.Actor,
			Entity:        st.Table,
			EntityID:      id,
			Op:            op,
			Diff:          diff,
		}
		// Same connection, same transaction: NewDB drops the outer
		// statement's clauses but keeps its ConnPool.
		if err := db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Create(&c).Error; err != nil {
			_ = db.AddError(fmt.Errorf("changes: record %s %s: %w", op, st.Table, err))
			return
		}
		t.recorded = append(t.recorded, c)
	}
}

func diffOf(dest any, row reflect.Value, op string) string {
	var v any
	switch op {
	case "update":
		// The written fields: Updates(map) or Updates(struct).
		v = dest
		if m, ok := dest.(map[string]any); ok {
			v = m
		}
	case "delete":
		// The row as the caller passed it: load it first and the
		// trail keeps what was deleted, not only its key.
		v = row.Interface()
	default:
		v = row.Interface()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"unmarshalable"}`
	}
	return string(b)
}
