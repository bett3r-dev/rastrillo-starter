// Package ledger keeps the prompt ledger amadan kept, without amadan:
// what was asked, what the agent said and did, what it cost, and which
// commits and merges came of it, stored in this repository's own git
// objects so any remote that carries refs carries the ledger too.
//
// The format is amadan's v1, read off the 3,559 records of
// rastrillo's public mirror (refs/amadan/ledger/*), so a ledger written
// here reads the same as one written there:
//
//   - one ref per branch, refs/amadan/ledger/<branch>;
//   - one commit per record, on the empty tree, its message one line
//     of JSON, its first parent the record before it;
//   - a merge-marker's second parent is the branch tip it merged, which
//     keeps a squashed branch's commits reachable after the branch is
//     deleted;
//   - author and committer amadan-ledger <ledger@amadan>, dated at the
//     record's ts.
//
// Six record types: prompt, reply, tool, usage (from the agent's
// session transcript), commit-marker (from the post-commit hook) and
// merge-marker (from Merge). A tool record is the tool's name and its
// input, never its output, cut to 2,048 characters as amadan cut it.
package ledger

import (
	"encoding/json"
	"strings"
	"time"
)

// Record is one ledger entry. The field order is amadan's, and
// omitempty leaves each type with exactly its own keys.
type Record struct {
	V         int     `json:"v"`
	Type      string  `json:"type"`
	TS        string  `json:"ts"`
	Origin    string  `json:"origin,omitempty"`
	Author    string  `json:"author,omitempty"`
	Session   string  `json:"session,omitempty"`
	Model     string  `json:"model,omitempty"`
	Text      string  `json:"text,omitempty"`
	Tokens    *Tokens `json:"tokens,omitempty"`
	SHA       string  `json:"sha,omitempty"`
	Subject   string  `json:"subject,omitempty"`
	MsgOrigin string  `json:"msg_origin,omitempty"`
	Target    string  `json:"target,omitempty"`
	Via       string  `json:"via,omitempty"`
	BranchTip string  `json:"branch_tip,omitempty"`
}

// Tokens is a usage record's count, summed over one flush.
type Tokens struct {
	In        int64 `json:"in"`
	CacheRead int64 `json:"cache_read"`
	CacheNew  int64 `json:"cache_new"`
	Out       int64 `json:"out"`
}

// The record types.
const (
	TypePrompt       = "prompt"
	TypeReply        = "reply"
	TypeTool         = "tool"
	TypeUsage        = "usage"
	TypeCommitMarker = "commit-marker"
	TypeMergeMarker  = "merge-marker"
)

// ToolTextMax is where a tool record's text is cut, ellipsis included.
const ToolTextMax = 2048

// Stamp formats t the way every record's ts is written.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (r Record) encode() (string, error) {
	r.V = 1
	b, err := json.Marshal(r)
	return string(b), err
}

func decode(msg string) (Record, error) {
	var r Record
	err := json.Unmarshal([]byte(strings.TrimSpace(msg)), &r)
	return r, err
}

// cut shortens s to max characters, the last of them an ellipsis.
func cut(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max-1]) + "…"
}
