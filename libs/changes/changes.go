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
// A Trail can also say what larger thing its writes belong to, with
// Within("session:12"): every change it records then carries that
// scope, deletes and update diffs included, and InScope reads them
// back in id order after a cursor. That is how something bigger than
// one row finds all of its own changes without a join across
// contexts. A Trail never scoped records "".
//
// The thing a Trail is within is sometimes the row it is about to
// create, whose id only the insert knows: WithinCreated("session")
// scopes the Trail to "session:<id>" of the row its next tracked
// write creates, that change included, so starting a session lands
// in the session's own history like every change after it.
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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"amadan.net/rastrillo/rastrillo/migrate"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
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
	// Actor is "member:<id>", "platform:<id>" (a platform admin),
	// "human" (signing up, before there is a member) or "agent:<name>"
	// ("agent:claude-code:<member>" for an attached session, ADR 0007).
	Actor string `gorm:"not null"`
	// OnBehalfOf is as whom Actor made the change, when that is
	// someone else: "member:12" while platform admin "platform:7" acts
	// as that member (ADR 0005). "" when Actor acted as itself. Actor
	// always stays who really did it.
	OnBehalfOf string `gorm:"index;not null;default:''"`
	// EntryPoint is the business name of what opened the write: a
	// person's command (WriteNote) or an outside fact
	// (BankReportedTransaction, DayPassed). A Trail opened with no name
	// records the request's method and chi route pattern instead
	// ("POST /notes/{id}/archive"), so an unnamed action still shows in
	// the app's docs/flow.md. Every change a request lands carries the
	// Trail's, the caused ones included, so the flow chain reads
	// Command -> Event ==> Event off this table alone.
	EntryPoint string `gorm:"index;not null;default:''"`
	// Scope is the larger thing this change belongs to, "kind:id"
	// ("session:12"), set by Trail.Within; "" when unscoped. Every
	// change a scoped Trail records carries it, whatever the table or
	// op, so InScope finds a session's own changes by one equality.
	Scope string `gorm:"index;not null;default:''"`
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

// ErrBadScope is the refusal of a scope that is not "kind:id": a kind
// of lowercase letters, digits, '-' or '_' starting with a letter, a
// colon, and an id with no whitespace ("session:12"). Within with one
// makes every write of its Trail refused; InScope refuses it outright.
var ErrBadScope = errors.New(`changes: scope is not "kind:id"`)

// ErrScopeConflict is the refusal of Within on a Trail already scoped
// to something else: a change belongs to one larger thing, and moving
// a Trail's writes from one to another midway would split them.
var ErrScopeConflict = errors.New("changes: Trail is already within another scope")

// ErrScopePending is the refusal of a tracked write, through a Trail
// waiting on WithinCreated, that is not the one-row create naming its
// scope: the scope's own row must come first, so nothing the Trail
// writes is left outside it.
var ErrScopePending = errors.New("changes: Trail is within the row it creates next, and this write is not that one-row create")

// ErrNotAdmitted is the refusal of a tracked write that its Trail's
// scope does not take (Policy.Admit said no): a frozen session's, say.
// It wraps Admit's own error, so errors.Is finds either.
var ErrNotAdmitted = errors.New("changes: the scope does not take this write")

// ErrReasonMismatch is the refusal of a write its reason does not
// fit: a table the reason does not declare, or a column on it the
// reason does not declare (Policy.Allow). A right-context reason on
// the wrong row (a correction written as NoteWritten) is still a
// wrong reason.
var ErrReasonMismatch = errors.New("changes: reason does not fit the write")

var scopeFormat = regexp.MustCompile(`^[a-z][a-z0-9_-]*:\S+$`)

var scopeKind = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

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
	// EntryPoint names what opened this Trail, in business words, set
	// where the Trail is opened. Written on every change it records;
	// left "", the hook writes the request's route (see entryPoint).
	EntryPoint string
	// OnBehalfOf is as whom Actor writes ("member:12"), set where the
	// Trail is opened when a platform admin acts as a member. Written
	// on every change it records; "" when Actor acts as itself.
	OnBehalfOf string

	scope    string // set by Within; "" means unscoped
	scopeErr error  // a refused Within: every write is refused with it
	// pending is WithinCreated's kind until the create it waits on
	// lands and sets scope; "" when nothing is pending.
	pending string

	reason string
	cause  string // as given to Because; "" means the request itself
	// Resolved by require for the statement in flight, used by record.
	correlation string
	stmtCause   string
	requestID   string // the chain's root
	recorded    []Change
	// onRecord is OnRecord's: told each change as it is recorded, on
	// the write's own connection.
	onRecord []func(db *gorm.DB, c Change) error
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

// Within scopes every write that follows to the larger thing they
// belong to, "kind:id" ("session:12"), and returns t, so it chains
// like Because. Scoping is for the Trail's whole life: Within again
// with the same scope does nothing, and with another one, or with a
// malformed one, is not undone by anything later; every tracked write
// through t is then refused (ErrScopeConflict, ErrBadScope) before it
// reaches SQLite, so no write of a misscoped Trail lands at all. The
// refusal waits for the write, as ErrBadCause does, so a handler can
// chain Within where the Trail opens.
func (t *Trail) Within(scope string) *Trail {
	switch {
	case t.scopeErr != nil:
	case !scopeFormat.MatchString(scope):
		t.scopeErr = fmt.Errorf("%w: %q", ErrBadScope, scope)
	case t.pending != "":
		t.scopeErr = fmt.Errorf("%w: within the %s created next, not %s", ErrScopeConflict, t.pending, scope)
	case t.scope != "" && t.scope != scope:
		t.scopeErr = fmt.Errorf("%w: %s, not %s", ErrScopeConflict, t.scope, scope)
	default:
		t.scope = scope
	}
	return t
}

// WithinCreated is Within for a thing that does not exist yet: the
// Trail's next tracked write must create exactly one row, and that
// row's id names the scope, "kind:<id>" (WithinCreated("session"),
// then the insert of session 7, is "session:7"). The create's own
// change carries the scope, and so does every change after it, as
// with Within. Any other tracked write first (an update, a delete, a
// create of several rows) is refused before it reaches SQLite
// (ErrScopePending), and the Trail keeps waiting. A malformed kind
// (ErrBadScope), or a Trail already scoped or already waiting
// (ErrScopeConflict), refuses every write of the Trail, as a refused
// Within does.
//
// It is how the insert's id becomes the scope with no allocation
// ahead of the insert and no second write after it. The created row
// must have an integer primary key the insert assigns; a create that
// yields none is an error only after the row is written, so make it
// inside a transaction.
func (t *Trail) WithinCreated(kind string) *Trail {
	switch {
	case t.scopeErr != nil:
	case !scopeKind.MatchString(kind):
		t.scopeErr = fmt.Errorf("%w: kind %q", ErrBadScope, kind)
	case t.pending != "" || t.scope != "":
		t.scopeErr = fmt.Errorf("%w: %s%s, not a new %s", ErrScopeConflict, t.scope, t.pending, kind)
	default:
		t.pending = kind
	}
	return t
}

// OnRecord asks to be told every change this Trail records, as it is
// recorded: fn runs right after the change row is written, on the
// same connection and so inside the write's transaction, with db a
// session of that connection under no hook. An error from fn refuses
// the write as any hook refusal does, so what fn writes lands with
// the change or not at all: a ledger beside the record (the agent
// door's submissions, apps/server) cannot hold an id whose change was
// rolled back, nor miss one that committed. Several calls add several
// functions, run in order.
func (t *Trail) OnRecord(fn func(db *gorm.DB, c Change) error) *Trail {
	t.onRecord = append(t.onRecord, fn)
	return t
}

// Scope is what Within set, "" when the Trail is unscoped.
func (t *Trail) Scope() string { return t.scope }

// InScope is the scope's changes with id above afterID, in id order:
// "this session's changes since cursor X". afterID 0 is all of them.
// An empty or malformed scope is refused (ErrBadScope) rather than
// read as "every unscoped change".
func InScope(ctx context.Context, db *gorm.DB, scope string, afterID int64) ([]Change, error) {
	if !scopeFormat.MatchString(scope) {
		return nil, fmt.Errorf("%w: %q", ErrBadScope, scope)
	}
	var out []Change
	err := db.WithContext(ctx).Where("scope = ? AND id > ?", scope, afterID).Order("id").Find(&out).Error
	return out, err
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
	// Admit, when set, is asked before every tracked write through a
	// scoped Trail whether that scope takes it: the scope ("session:12"),
	// the write's reason, and db, a session on the statement's own
	// connection (a transaction's, inside one), so a check and the
	// write it guards cannot be split by another writer. An error
	// refuses the write before it reaches SQLite (ErrNotAdmitted,
	// wrapping it). A Trail still waiting on WithinCreated is not
	// asked: the thing it is within does not exist yet. A frozen
	// session refusing every write but its reopening is the use.
	Admit func(db *gorm.DB, scope, reason string) error
	// Committed, when set, is told after each commit that landed
	// scoped changes which scopes they were in ("session:12"), once
	// each, sorted; never inside the transaction, never for a rollback
	// (committed.go). It runs on the writer's goroutine right after the
	// commit, so it must not block: a doorbell's non-blocking ring is
	// the use. Unscoped changes are not told. Left nil, the handle's
	// pool is not wrapped.
	Committed func(scopes []string)
	// Allow is what each reason may write: per table, the columns its
	// change's diff may carry, by column name. A write whose reason is
	// not here, whose table the reason does not name, or whose diff
	// carries a column outside the list, is refused before SQLite
	// (ErrReasonMismatch). Nil turns the check off.
	Allow map[string]Writes
}

// Writes is one reason's declaration: table -> the columns it writes.
type Writes map[string][]string

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
	if p.Committed != nil {
		if err := installCommitted(g, p.Committed); err != nil {
			return err
		}
	}
	cb := func(op string) func(*gorm.DB) {
		return func(db *gorm.DB) { record(db, set, op, p.Committed) }
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
	if err := p.fits(st, t.reason, op); err != nil {
		_ = db.AddError(err)
		return
	}
	if t.scopeErr != nil {
		_ = db.AddError(fmt.Errorf("%w (%s %s)", t.scopeErr, op, st.Table))
		return
	}
	if t.pending != "" && (op != "create" || rowCount(st.ReflectValue) != 1) {
		_ = db.AddError(fmt.Errorf("%w: within the %s created next (%s %s)", ErrScopePending, t.pending, op, st.Table))
		return
	}
	if p.Admit != nil && t.scope != "" {
		if err := p.Admit(db.Session(&gorm.Session{NewDB: true, SkipHooks: true}), t.scope, t.reason); err != nil {
			_ = db.AddError(fmt.Errorf("%w: %s (%s %s): %w", ErrNotAdmitted, t.scope, op, st.Table, err))
			return
		}
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

// fits refuses a write its reason does not declare: the table, then
// each column the change's diff will carry (the same diffOf record
// writes, keys turned into column names by the schema).
func (p Policy) fits(st *gorm.Statement, reason, op string) error {
	if p.Allow == nil {
		return nil
	}
	cols, ok := p.Allow[reason][st.Table]
	if !ok {
		var tables []string
		for t := range p.Allow[reason] {
			tables = append(tables, t)
		}
		slices.Sort(tables)
		return fmt.Errorf("%w: %s does not write %s (it writes %s)", ErrReasonMismatch, reason, st.Table, strings.Join(tables, ", "))
	}
	for _, row := range rowsOf(st) {
		var diff map[string]json.RawMessage
		if err := json.Unmarshal([]byte(diffOf(st.Dest, row, op)), &diff); err != nil {
			return fmt.Errorf("%w: %s on %s: %v", ErrReasonMismatch, reason, st.Table, err)
		}
		keys := make([]string, 0, len(diff))
		for k := range diff {
			keys = append(keys, columnOf(st.Schema, k))
		}
		slices.Sort(keys)
		for _, k := range keys {
			if !slices.Contains(cols, k) {
				return fmt.Errorf("%w: %s does not write %s.%s", ErrReasonMismatch, reason, st.Table, k)
			}
		}
	}
	return nil
}

// columnOf is the column a diff key names. The key is what diffOf's
// json.Marshal wrote: the field's JSON tag when it has one (a model
// whose JSON is an API, `json:"esId"`), else its Go name; an
// Updates(map) key may be the column itself. A key that names no
// field stays as it is, and fits refuses it as a column no reason
// declares.
func columnOf(s *schema.Schema, key string) string {
	if f := s.LookUpField(key); f != nil && f.DBName != "" {
		return f.DBName
	}
	for _, f := range s.Fields {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == key && f.DBName != "" {
			return f.DBName
		}
	}
	return key
}

// rowsOf is the statement's rows: one, or each of a slice.
func rowsOf(st *gorm.Statement) []reflect.Value {
	rv := st.ReflectValue
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return []reflect.Value{rv}
	}
	rows := make([]reflect.Value, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		rows = append(rows, rv.Index(i))
	}
	return rows
}

// rowCount is how many rows a statement's value holds: a slice's
// length, otherwise one.
func rowCount(rv reflect.Value) int {
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return rv.Len()
	}
	return 1
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

func record(db *gorm.DB, tracked map[string]bool, op string, committed func([]string)) {
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
	for _, row := range rowsOf(st) {
		var id int64
		if pk := st.Schema.PrioritizedPrimaryField; pk != nil {
			if v, zero := pk.ValueOf(st.Context, row); !zero {
				if n, ok := v.(int64); ok {
					id = n
				}
			}
		}
		if t.pending != "" {
			// require let only a one-row create through: its id is
			// the scope, for this change and every one after it.
			if id == 0 {
				_ = db.AddError(fmt.Errorf("%w: the %s created on %s has no id", ErrBadScope, t.pending, st.Table))
				return
			}
			t.scope, t.pending = fmt.Sprintf("%s:%d", t.pending, id), ""
		}
		diff := diffOf(st.Dest, row, op)
		c := Change{
			Reason:        t.reason,
			Cause:         t.stmtCause,
			RequestID:     root,
			CorrelationID: t.correlation,
			Actor:         t.Actor,
			OnBehalfOf:    t.OnBehalfOf,
			Scope:         t.scope,
			EntryPoint:    entryPoint(t, st.Context),
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
		for _, fn := range t.onRecord {
			if err := fn(db.Session(&gorm.Session{NewDB: true, SkipHooks: true}), c); err != nil {
				_ = db.AddError(fmt.Errorf("changes: on record %s %s: %w", op, st.Table, err))
				return
			}
		}
	}
	landed(db, t.scope, committed)
}

// entryPoint is the Trail's name for what opened it or, when it has
// none, the method and chi route pattern of the request the write
// runs in ("POST /notes/{id}/archive"). Outside a routed request it
// stays "".
func entryPoint(t *Trail, ctx context.Context) string {
	if t.EntryPoint != "" || ctx == nil {
		return t.EntryPoint
	}
	rc := chi.RouteContext(ctx)
	if rc == nil || rc.RoutePattern() == "" {
		return ""
	}
	if rc.RouteMethod == "" {
		return rc.RoutePattern()
	}
	return rc.RouteMethod + " " + rc.RoutePattern()
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
