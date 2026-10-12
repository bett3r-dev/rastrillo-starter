package myapp

// The accounts context: who is on this instance. Its vocabulary lands
// on the tables it owns and on no other. The glossary is
// CONTEXT-accounts.md.

const ContextAccounts = "accounts"

var accountsTables = []string{"members"}

const (
	// members
	ReasonMemberJoined = "MemberJoined"
)

var accountsReasons = []string{
	ReasonMemberJoined,
}

// accountsWrites is what each reason may write: per table, the columns
// its change's diff may carry (changes.Policy.Allow). A write outside
// it is refused (ErrReasonMismatch). A create's diff is the whole row,
// so a reason that creates on a table lists every column of it; the
// hash is in the diff only as empty, written exempt in a second step.
var accountsWrites = map[string]Writes{
	ReasonMemberJoined: {
		"members": {"id", "email", "name", "password_hash", "created_at", "updated_at"},
	},
}
