# notes

The example context: replace it with the app's first real one, and
keep the shape (a reasons file, this glossary, scenarios). Change a
word's meaning here in the same commit as the code that changes it.

| Word | Means | Not |
| --- | --- | --- |
| note | a title and a body a member wrote down, shared with everyone on the instance | a private draft |
| author | the member who wrote the note; identity by id only (`author_id`), never joined to `members` | the member who last corrected it |
| correction | a note's text put right by a new change (`NoteCorrected`) whose cause is the change it corrects | an edit that rewrites history |

Tables: `notes`. Reasons: `reasons_notes.go`.
