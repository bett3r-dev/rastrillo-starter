package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agent says who is committing: the Claude Code session, if the commit
// was made from inside one. Claude Code exports CLAUDECODE=1 and
// CLAUDE_CODE_SESSION_ID to every command it runs, git hooks included.
type Agent struct {
	Session string
}

// AgentFromEnv reads Agent off the environment; zero is a person.
func AgentFromEnv() Agent {
	if os.Getenv("CLAUDECODE") != "1" {
		return Agent{}
	}
	return Agent{Session: os.Getenv("CLAUDE_CODE_SESSION_ID")}
}

// CommitMarker records HEAD on the ledger of the branch it landed on:
// its sha and subject, the committer's email, and whether an agent
// wrote it (msg_origin llm, with its session) or a person did (human).
// A commit on a detached HEAD belongs to no branch and is not marked.
func (r Repo) CommitMarker(a Agent) error {
	branch := r.Branch()
	if branch == "" {
		return nil
	}
	out, err := r.git("", nil, "log", "-1", "--format=%H%x00%ce%x00%cI%x00%s", "HEAD")
	if err != nil {
		return err
	}
	f := strings.SplitN(out, "\x00", 4)
	if len(f) != 4 {
		return fmt.Errorf("ledger: cannot read HEAD: %q", out)
	}
	ts := f[2]
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		ts = Stamp(t)
	}
	rec := Record{Type: TypeCommitMarker, TS: ts, Author: f[1], SHA: f[0], Subject: f[3], MsgOrigin: "human"}
	if a.Session != "" {
		rec.Session, rec.MsgOrigin = a.Session, "llm"
	}
	return r.Append(branch, []Record{rec})
}

// Merge lands branch on the checked-out branch, the way amadan branch
// merge did. squash writes one commit whose message is msg (or, when
// msg is empty and the branch has one commit, that commit's message),
// then the branch's name, every prompt on its ledger, and the line
// that says it was squashed. Either way a merge-marker lands on the
// branch's ledger, its second parent the branch tip, so the branch's
// own commits stay reachable after the branch is deleted.
func (r Repo) Merge(branch, msg string, squash bool) (string, error) {
	target := r.Branch()
	if target == "" {
		return "", errors.New("ledger: merge needs a checked-out branch to land on")
	}
	if target == branch {
		return "", errors.New("ledger: cannot merge a branch into itself")
	}
	tip, err := r.git("", nil, "rev-parse", "--verify", branch+"^{commit}")
	if err != nil {
		return "", err
	}
	via := "merge"
	if squash {
		via = "squash"
		if msg == "" {
			n, err := r.git("", nil, "rev-list", "--count", target+".."+tip)
			if err != nil {
				return "", err
			}
			if n != "1" {
				return "", fmt.Errorf("ledger: %s has %s commits to squash; say what they add up to with -m", branch, n)
			}
			if msg, err = r.git("", nil, "log", "-1", "--format=%B", tip); err != nil {
				return "", err
			}
		}
		recs, err := r.Read(branch)
		if err != nil {
			return "", err
		}
		if _, err := r.git("", nil, "merge", "--squash", tip); err != nil {
			return "", err
		}
		if _, err := r.git(SquashMessage(msg, branch, target, recs), nil, "commit", "--file=-"); err != nil {
			return "", err
		}
	} else {
		args := []string{"merge", "--no-ff", "--no-edit", tip}
		if msg != "" {
			args = []string{"merge", "--no-ff", "-m", msg, tip}
		}
		if _, err := r.git("", nil, args...); err != nil {
			return "", err
		}
	}
	sha, err := r.git("", nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	marker := Record{Type: TypeMergeMarker, TS: Stamp(time.Now()), SHA: sha, Target: target, Via: via, BranchTip: tip}
	return sha, r.Append(branch, []Record{marker})
}

// SquashMessage is a squash commit's message: msg, the branch, the
// prompts that produced it (under "Prompt:" or "Prompts:", each
// indented two spaces and set apart by a blank line), and where it was
// squashed from.
func SquashMessage(msg, branch, target string, recs []Record) string {
	var prompts []string
	for _, r := range recs {
		if r.Type == TypePrompt {
			prompts = append(prompts, r.Text)
		}
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(msg, "\n"))
	fmt.Fprintf(&b, "\n\nBranch: %s\n", branch)
	if len(prompts) > 0 {
		label := "Prompts:"
		if len(prompts) == 1 {
			label = "Prompt:"
		}
		fmt.Fprintf(&b, "\n%s\n", label)
		for _, p := range prompts {
			b.WriteString("\n")
			for _, line := range strings.Split(p, "\n") {
				b.WriteString(strings.TrimRight("  "+line, " ") + "\n")
			}
		}
	}
	fmt.Fprintf(&b, "\nSquashed from %s into %s.\n", branch, target)
	return b.String()
}

// Logf appends a line to the ledger's error log, for the hooks: they
// must never fail the commit or the turn they run after, so what goes
// wrong is written down instead of raised.
func (r Repo) Logf(format string, args ...any) {
	state, err := r.StateDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(state, "errors.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{Stamp(time.Now())}, args...)...)
}
