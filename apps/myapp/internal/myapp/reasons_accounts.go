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
