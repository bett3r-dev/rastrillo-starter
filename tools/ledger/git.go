package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RefPrefix is where every branch's ledger lives.
const RefPrefix = "refs/amadan/ledger/"

// emptyTree is git's well-known empty tree: every record commit has it.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Ref is the ledger ref of branch.
func Ref(branch string) string { return RefPrefix + branch }

// Repo is a git working tree the ledger is written into.
type Repo struct{ Dir string }

func (r Repo) git(stdin string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Branch is the branch checked out, or "" on a detached HEAD.
func (r Repo) Branch() string {
	b, err := r.git("", nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return b
}

// Email is the configured user.email, or "" if none is set.
func (r Repo) Email() string {
	e, _ := r.git("", nil, "config", "user.email")
	return e
}

// StateDir is where this machine keeps its own bookkeeping (how far
// each session's transcript has been read). It sits in git's common
// directory, so every worktree of the repository shares it and none
// of it is ever committed or pushed.
func (r Repo) StateDir() (string, error) {
	d, err := r.git("", nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	d = filepath.Join(d, "amadan-ledger")
	return d, os.MkdirAll(d, 0o700)
}

func (r Repo) tip(ref string) string {
	t, err := r.git("", nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return t
}

// ErrMoved is a lost race: another writer moved the ref first.
var ErrMoved = errors.New("ledger: ref moved while appending")

// Append writes recs onto branch's ledger, in order, as one atomic
// move of the ref: a concurrent writer makes it retry, never
// interleave or lose records.
func (r Repo) Append(branch string, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	if branch == "" {
		return errors.New("ledger: no branch to append to")
	}
	for range 5 {
		err := r.append(Ref(branch), recs)
		if !errors.Is(err, ErrMoved) {
			return err
		}
	}
	return ErrMoved
}

func (r Repo) append(ref string, recs []Record) error {
	old := r.tip(ref)
	parent := old
	for _, rec := range recs {
		msg, err := rec.encode()
		if err != nil {
			return err
		}
		args := []string{"commit-tree", "--no-gpg-sign", emptyTree}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		if rec.Type == TypeMergeMarker && rec.BranchTip != "" {
			args = append(args, "-p", rec.BranchTip)
		}
		env := []string{
			"GIT_AUTHOR_NAME=amadan-ledger", "GIT_AUTHOR_EMAIL=ledger@amadan", "GIT_AUTHOR_DATE=" + rec.TS,
			"GIT_COMMITTER_NAME=amadan-ledger", "GIT_COMMITTER_EMAIL=ledger@amadan", "GIT_COMMITTER_DATE=" + rec.TS,
		}
		if parent, err = r.git(msg+"\n", env, args...); err != nil {
			return err
		}
	}
	// An empty old value makes update-ref create the ref only if it
	// still does not exist: the compare half of compare-and-swap.
	if _, err := r.git("", nil, "update-ref", "-m", "ledger: append", ref, parent, old); err != nil {
		if r.tip(ref) != old {
			return ErrMoved
		}
		return err
	}
	return nil
}

// Read returns branch's ledger, oldest record first.
func (r Repo) Read(branch string) ([]Record, error) {
	ref := Ref(branch)
	if r.tip(ref) == "" {
		return nil, nil
	}
	out, err := r.git("", nil, "log", "--first-parent", "--reverse", "--format=%B%x00", ref)
	if err != nil {
		return nil, err
	}
	var recs []Record
	for _, msg := range strings.Split(out, "\x00") {
		if strings.TrimSpace(msg) == "" {
			continue
		}
		rec, err := decode(msg)
		if err != nil {
			return nil, fmt.Errorf("ledger %s: %w", branch, err)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// Branches lists every branch that has a ledger.
func (r Repo) Branches() ([]string, error) {
	out, err := r.git("", nil, "for-each-ref", "--format=%(refname)", RefPrefix)
	if err != nil || out == "" {
		return nil, err
	}
	var bs []string
	for _, ref := range strings.Split(out, "\n") {
		bs = append(bs, strings.TrimPrefix(ref, RefPrefix))
	}
	return bs, nil
}
