# accounts

Who is on this instance. Change a word's meaning here in the same
commit as the code that changes it.

| Word | Means | Not |
| --- | --- | --- |
| member | a person signed up on this instance; one instance is one tenant | a user of another instance |
| joining | signing up: the member's row and its `MemberJoined` change, in one transaction (with the notes context's welcome note, caused by it) | signing in, which is not a business fact |

Tables: `members`. Reasons: `reasons_accounts.go`.
