package myapp

import "time"

// Models is every model the schema generator manages. Keep it in step
// with the structs below: `rastrillo migration generate` reads
// it to work out what the database should look like, and
// `rastrillo migration check` fails CI when it and the
// migrations disagree.
var Models = []any{
	&Note{},
}

// Note is a starting example model — rename it, replace it, add more
// beside it. Whatever it becomes, the rule stays the same: every query
// touching user-owned rows goes through scope.Owned (SKILL.md) — reads
// AND writes — and a request body is never bound onto a model directly
// (explicit map[string]any + .Select allowlist).
type Note struct {
	ID        int64
	Title     string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
