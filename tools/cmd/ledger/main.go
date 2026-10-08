// Command ledger keeps this repository's prompt ledger: amadan's, in
// amadan's format, without amadan (see tools/ledger).
//
//	ledger install                    point git at .githooks, fetch ledgers from every remote
//	ledger log [-json] [branch]       read a branch's ledger, oldest first (default: this branch)
//	ledger branches                   list the branches that have a ledger
//	ledger merge [-squash] [-m msg] <branch>
//	                                  land branch here, carrying its prompts, and mark it
//	ledger sync <transcript.jsonl>... add what those sessions said and did (backfill)
//	ledger hook                       Claude Code's Stop/SessionEnd hook (JSON on stdin)
//	ledger post-commit                git's post-commit hook
//
// The two hooks never fail what they run after: a turn ends and a
// commit lands whatever the ledger does, and what went wrong is
// written to .git/amadan-ledger/errors.log.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"myproject/tools/ledger"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	repo := ledger.Repo{Dir: "."}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "hook":
		hook(repo)
		return
	case "post-commit":
		postCommit(repo)
		return
	case "install":
		err = install()
	case "log":
		err = logCmd(repo, args)
	case "branches":
		var bs []string
		if bs, err = repo.Branches(); err == nil && len(bs) > 0 {
			fmt.Println(strings.Join(bs, "\n"))
		}
	case "merge":
		err = merge(repo, args)
	case "sync":
		for _, p := range args {
			if err = repo.Sync(p); err != nil {
				break
			}
		}
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ledger:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ledger install | log [-json] [branch] | branches | merge [-squash] [-m msg] <branch> | sync <transcript>... | hook | post-commit")
	os.Exit(2)
}

// hook reads Claude Code's hook input and syncs that session.
func hook(repo ledger.Repo) {
	var in struct {
		TranscriptPath string `json:"transcript_path"`
		Cwd            string `json:"cwd"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		repo.Logf("hook: reading input: %v", err)
		return
	}
	if in.Cwd != "" {
		repo.Dir = in.Cwd
	}
	if err := repo.Sync(in.TranscriptPath); err != nil {
		repo.Logf("hook: %v", err)
	}
}

// postCommit flushes the committing session first, so the prompts and
// tool calls that led to the commit sit before its marker.
func postCommit(repo ledger.Repo) {
	a := ledger.AgentFromEnv()
	if p := ledger.Transcript(a.Session); p != "" {
		if err := repo.Sync(p); err != nil {
			repo.Logf("post-commit: sync: %v", err)
		}
	}
	if err := repo.CommitMarker(a); err != nil {
		repo.Logf("post-commit: %v", err)
	}
}

func logCmd(repo ledger.Repo, args []string) error {
	fs := flag.NewFlagSet("log", flag.ExitOnError)
	raw := fs.Bool("json", false, "print each record as its JSON line")
	_ = fs.Parse(args)
	branch := fs.Arg(0)
	if branch == "" {
		branch = repo.Branch()
	}
	recs, err := repo.Read(branch)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if *raw {
			b, _ := json.Marshal(r)
			fmt.Println(string(b))
			continue
		}
		fmt.Println(line(r))
	}
	return nil
}

// line is one record as a person skims it.
func line(r ledger.Record) string {
	text := r.Text
	switch r.Type {
	case ledger.TypeUsage:
		if t := r.Tokens; t != nil {
			text = fmt.Sprintf("%s in %d, cache read %d, cache new %d, out %d", r.Model, t.In, t.CacheRead, t.CacheNew, t.Out)
		}
	case ledger.TypeCommitMarker:
		text = fmt.Sprintf("%.7s %s (%s)", r.SHA, r.Subject, r.MsgOrigin)
	case ledger.TypeMergeMarker:
		text = fmt.Sprintf("%.7s into %s by %s, tip %.7s", r.SHA, r.Target, r.Via, r.BranchTip)
	}
	text = strings.Join(strings.Fields(text), " ")
	if rs := []rune(text); len(rs) > 140 {
		text = string(rs[:139]) + "…"
	}
	return fmt.Sprintf("%s  %-13s %s", r.TS, r.Type, text)
}

func merge(repo ledger.Repo, args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	squash := fs.Bool("squash", false, "land as one commit carrying the branch's prompts")
	msg := fs.String("m", "", "the commit message")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	sha, err := repo.Merge(fs.Arg(0), *msg, *squash)
	if err == nil {
		fmt.Println(sha)
	}
	return err
}

// install wires the post-commit hook and, for every remote, a fetch
// refspec that brings the ledgers down with the branches.
func install() error {
	if err := run("git", "config", "core.hooksPath", ".githooks"); err != nil {
		return err
	}
	out, err := exec.Command("git", "remote").Output()
	if err != nil {
		return err
	}
	const spec = "+" + ledger.RefPrefix + "*:" + ledger.RefPrefix + "*"
	for _, remote := range strings.Fields(string(out)) {
		have, _ := exec.Command("git", "config", "--get-all", "remote."+remote+".fetch").Output()
		if strings.Contains(string(have), spec) {
			continue
		}
		if err := run("git", "config", "--add", "remote."+remote+".fetch", spec); err != nil {
			return err
		}
	}
	fmt.Println("ledger: post-commit hook on (.githooks), ledgers fetched from every remote")
	return nil
}

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}
