package myapp

import "time"

// Models is every model the schema generator manages. Keep it in step
// with the structs below: `rastrillo migration generate` reads
// it to work out what the database should look like, and
// `rastrillo migration check` fails CI when it and the
// migrations disagree. changes.Change is not here: its table is a
// subsystem's (changes.Schema, merged into BootSchema).
var Models = []any{
	&Member{},
	&Note{},
}

// Tracked is every table whose writes must be explained by a change
// record (changes.Install). members is tracked too: signing up is a
// business fact. Every table in the database is in exactly one of
// Tracked and Untracked: changes.Install refuses at boot a table in
// neither (the boot census), and TestEveryTableIsTrackedOrDeclaredNot
// holds it first, so a new table has to be placed on purpose.
var Tracked = []string{"members", "notes"}

// Untracked is every table whose writes are not explained, each with
// the reason it is plumbing: what it did lands on a tracked table.
var Untracked = map[string]string{
	"sessions":             "rastrillo's session store; signing in is not a business fact",
	"changes":              "the change record itself; append-only by trigger",
	"rastrillo_migrations": "the migration ledger",
	"sqlite_sequence":      "SQLite's AUTOINCREMENT bookkeeping",
}

// TrackedExempt is the one column a tracked table may be written
// without a change record: the password hash, which createMember
// writes in a second step so the MemberJoined diff carries no
// secret. The database guard (changes.Install) lets an UPDATE of
// that column alone through and refuses every other unexplained
// write on members.
var TrackedExempt = map[string][]string{"members": {"password_hash"}}

// Member is one person signed up on this instance. One instance is
// one tenant, so there is no tenant table: everyone signed in here is
// in it. The password plugin's Subject is the Member's id.
type Member struct {
	ID           int64
	Email        string `gorm:"uniqueIndex"`
	Name         string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Note is the example model: rename it, replace it, add more beside
// it. A mistake in one is put right by a correction (NoteCorrected),
// a new change pointing at the one it corrects, never by rewriting
// the record. Rows are shared by everyone on the instance, so
// scope.Owned applies to nothing yet; the instance is the boundary.
type Note struct {
	ID        int64
	AuthorID  int64 `gorm:"index"`
	Title     string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
