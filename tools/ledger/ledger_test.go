package ledger_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"

	"myproject/tools/ledger"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

// repo is a fresh repository on main with one commit.
func repo(t *testing.T) ledger.Repo {
	t.Helper()
	r := ledger.Repo{Dir: t.TempDir()}
	git(t, r, "init", "-q", "-b", "main")
	git(t, r, "config", "user.email", "ana@example.com")
	git(t, r, "config", "user.name", "Ana")
	git(t, r, "config", "commit.gpgsign", "false")
	commit(t, r, "README", "start")
	return r
}

func git(t *testing.T, r ledger.Repo, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = r.Dir
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, r ledger.Repo, file, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, file), []byte(subject+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, r, "add", file)
	git(t, r, "commit", "-q", "-m", subject)
}

// A record is a commit on the empty tree by amadan-ledger, dated at
// its ts, whose message is the JSON line amadan wrote, key for key.
func TestARecordIsAmadansCommit(t *testing.T) {
	t.Parallel()
	r := repo(t)
	err := r.Append("main", []ledger.Record{
		{Type: ledger.TypeCommitMarker, TS: "2026-09-26T10:23:34Z", Author: "paul@keymail.dev", Session: "6a6756fd", SHA: "d864924", Subject: "clientip: count hops", MsgOrigin: "llm"},
		{Type: ledger.TypeUsage, TS: "2026-09-26T10:23:35Z", Session: "6a6756fd", Model: "claude-opus-5", Tokens: &ledger.Tokens{In: 32, CacheRead: 2608069, CacheNew: 18539, Out: 9149}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := ledger.Ref("main")
	if got := git(t, r, "log", "-1", "--format=%B", ref+"~1"); got != `{"v":1,"type":"commit-marker","ts":"2026-09-26T10:23:34Z","author":"paul@keymail.dev","session":"6a6756fd","sha":"d864924","subject":"clientip: count hops","msg_origin":"llm"}` {
		t.Errorf("commit-marker message:\n%s", got)
	}
	if got := git(t, r, "log", "-1", "--format=%B", ref); got != `{"v":1,"type":"usage","ts":"2026-09-26T10:23:35Z","session":"6a6756fd","model":"claude-opus-5","tokens":{"in":32,"cache_read":2608069,"cache_new":18539,"out":9149}}` {
		t.Errorf("usage message:\n%s", got)
	}
	if got := git(t, r, "log", "-1", "--format=%T|%an <%ae>|%cn|%aI", ref); got != "4b825dc642cb6eb9a060e54bf8d69288fbee4904|amadan-ledger <ledger@amadan>|amadan-ledger|2026-09-26T10:23:35Z" {
		t.Errorf("record commit: %s", got)
	}
	if got := git(t, r, "rev-list", "--count", ref); got != "2" {
		t.Errorf("records chained: %s commits, want 2", got)
	}
}

const transcript = `{"type":"user","timestamp":"2026-10-07T23:50:35.120Z","sessionId":"s1","gitBranch":"main","message":{"role":"user","content":"Store the ledger"}}
{"type":"user","isMeta":true,"timestamp":"2026-10-07T23:50:35.200Z","sessionId":"s1","gitBranch":"main","message":{"role":"user","content":"<local-command-caveat>"}}
{"type":"assistant","timestamp":"2026-10-07T23:50:40Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"hm"}],"usage":{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":10,"output_tokens":5}}}
{"type":"assistant","timestamp":"2026-10-07T23:50:41Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"Reading first."}],"usage":{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":10,"output_tokens":5}}}
{"type":"assistant","timestamp":"2026-10-07T23:50:42Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Bash","input":{ "command": "ls"}}],"usage":{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":10,"output_tokens":5}}}
{"type":"user","timestamp":"2026-10-07T23:50:43Z","sessionId":"s1","gitBranch":"main","message":{"role":"user","content":[{"type":"tool_result","content":"secret output"}]}}
{"type":"attachment","timestamp":"2026-10-07T23:50:43Z","sessionId":"s1"}
{"type":"assistant","timestamp":"2026-10-07T23:51:00Z","sessionId":"s1","gitBranch":"ledger","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"text","text":"On the branch now."}],"usage":{"input_tokens":1,"cache_read_input_tokens":50,"cache_creation_input_tokens":0,"output_tokens":7}}}
`

// A streamed model call is written as several lines whose usage grows
// as the reply streams: only the last line carries the final output
// count. The ledger counts each call once, at that last count, as
// run-metrics does; the first copy undercounted output about 3x on a
// real build (pv4 D46).
func TestAStreamedCallCountsItsFinalUsage(t *testing.T) {
	t.Parallel()
	streamed := `{"type":"assistant","timestamp":"2026-10-16T10:00:00Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"hm"}],"usage":{"input_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":40,"output_tokens":1}}}
{"type":"assistant","timestamp":"2026-10-16T10:00:01Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":40,"output_tokens":12}}}
{"type":"assistant","timestamp":"2026-10-16T10:00:02Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":3,"cache_read_input_tokens":900,"cache_creation_input_tokens":40,"output_tokens":250}}}
{"type":"assistant","timestamp":"2026-10-16T10:00:09Z","sessionId":"s1","gitBranch":"main","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"text","text":"Next."}],"usage":{"input_tokens":1,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0,"output_tokens":30}}}
`
	var lines [][]byte
	for l := range strings.SplitSeq(streamed, "\n") {
		lines = append(lines, []byte(l))
	}
	batches, err := ledger.Records(lines, "claude~ana@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	recs := batches[0].Records
	u := recs[len(recs)-1]
	if u.Type != ledger.TypeUsage {
		t.Fatalf("last record = %+v, want the usage", u)
	}
	if *u.Tokens != (ledger.Tokens{In: 4, CacheRead: 1900, CacheNew: 40, Out: 280}) {
		t.Errorf("usage = %+v, want m1's final copy (out 250) plus m2 (out 30): {In:4 CacheRead:1900 CacheNew:40 Out:280}", *u.Tokens)
	}
}

// A transcript becomes prompts, replies, tool calls and one usage per
// branch, each line on the ledger of the branch it was written on.
// Thinking, meta lines and tool output never reach the ledger, and a
// model call's usage counts once though every block repeats it.
func TestATranscriptBecomesRecordsPerBranch(t *testing.T) {
	t.Parallel()
	lines := [][]byte{}
	for l := range strings.SplitSeq(transcript, "\n") {
		lines = append(lines, []byte(l))
	}
	batches, err := ledger.Records(lines, "claude~ana@example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || batches[0].Branch != "main" || batches[1].Branch != "ledger" {
		t.Fatalf("batches: %+v", batches)
	}
	var got []string
	for _, r := range batches[0].Records {
		got = append(got, r.Type+":"+r.Text)
	}
	want := []string{"prompt:Store the ledger", "reply:Reading first.", `tool:Bash {"command":"ls"}`, "usage:"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("main's records:\n got %q\nwant %q", got, want)
	}
	p, u := batches[0].Records[0], batches[0].Records[3]
	if p.TS != "2026-10-07T23:50:35Z" || p.Origin != "human" || p.Author != "claude~ana@example.com" || p.Session != "s1" {
		t.Errorf("prompt: %+v", p)
	}
	if *u.Tokens != (ledger.Tokens{In: 2, CacheRead: 100, CacheNew: 10, Out: 5}) || u.Model != "claude-opus-5-5" {
		t.Errorf("usage counted the repeated call more than once: %+v", *u.Tokens)
	}
	if r := batches[1].Records; len(r) != 2 || r[0].Text != "On the branch now." || r[1].Tokens.Out != 7 {
		t.Errorf("ledger branch: %+v", r)
	}
}

// A tool record is cut at 2,048 characters, the last an ellipsis.
func TestALongToolCallIsCut(t *testing.T) {
	t.Parallel()
	line := `{"type":"assistant","timestamp":"2026-10-07T23:50:42Z","sessionId":"s1","gitBranch":"main","message":{"id":"m1","content":[{"type":"tool_use","name":"Write","input":{"content":"` + strings.Repeat("x", 3000) + `"}}]}}`
	batches, err := ledger.Records([][]byte{[]byte(line)}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	text := []rune(batches[0].Records[0].Text)
	if len(text) != ledger.ToolTextMax || text[len(text)-1] != '…' {
		t.Fatalf("tool text: %d characters ending %q", len(text), string(text[len(text)-1]))
	}
}

// Sync reads each session once, and waits for a line still being
// written rather than reading half of it.
func TestSyncReadsEachLineOnce(t *testing.T) {
	t.Parallel()
	r := repo(t)
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	first := `{"type":"user","timestamp":"2026-10-07T23:50:35Z","sessionId":"s1","gitBranch":"main","message":{"content":"one"}}` + "\n"
	second := `{"type":"user","timestamp":"2026-10-07T23:50:36Z","sessionId":"s1","gitBranch":"main","message":{"content":"two"}}`
	if err := os.WriteFile(path, []byte(first+second), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.Sync(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(first+second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Sync(path); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Read("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Text != "one" || recs[1].Text != "two" {
		t.Fatalf("records: %+v", recs)
	}
}

// A commit is marked on its branch: an agent's with its session and
// msg_origin llm, a person's with msg_origin human and no session.
func TestACommitIsMarkedWithWhoWroteIt(t *testing.T) {
	t.Parallel()
	r := repo(t)
	if err := r.CommitMarker(ledger.Agent{}); err != nil {
		t.Fatal(err)
	}
	commit(t, r, "a.txt", "agent work")
	if err := r.CommitMarker(ledger.Agent{Session: "s9"}); err != nil {
		t.Fatal(err)
	}
	recs, err := r.Read("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records: %+v", recs)
	}
	if h := recs[0]; h.Subject != "start" || h.MsgOrigin != "human" || h.Session != "" || h.Author != "ana@example.com" {
		t.Errorf("person's commit: %+v", h)
	}
	if a := recs[1]; a.Subject != "agent work" || a.MsgOrigin != "llm" || a.Session != "s9" || a.SHA != git(t, r, "rev-parse", "HEAD") {
		t.Errorf("agent's commit: %+v", a)
	}
}

// A squash lands one commit carrying every prompt on the branch's
// ledger, and the merge-marker keeps the branch's commits reachable
// after the branch is gone.
func TestASquashCarriesThePromptsAndKeepsTheBranch(t *testing.T) {
	t.Parallel()
	r := repo(t)
	git(t, r, "checkout", "-q", "-b", "split-rule")
	commit(t, r, "a.txt", "split: one")
	commit(t, r, "b.txt", "split: two")
	tip := git(t, r, "rev-parse", "HEAD")
	if err := r.Append("split-rule", []ledger.Record{
		{Type: ledger.TypePrompt, TS: "2026-10-07T10:00:00Z", Origin: "human", Text: "Split the rent\nby shares"},
		{Type: ledger.TypeReply, TS: "2026-10-07T10:00:01Z", Origin: "llm", Text: "Done."},
		{Type: ledger.TypePrompt, TS: "2026-10-07T10:05:00Z", Origin: "human", Text: "Name the rule"},
	}); err != nil {
		t.Fatal(err)
	}
	git(t, r, "checkout", "-q", "main")
	if _, err := r.Merge("split-rule", "", true); err == nil {
		t.Fatal("squashing two commits with no message: want a refusal")
	}
	sha, err := r.Merge("split-rule", "Split the rent by shares", true)
	if err != nil {
		t.Fatal(err)
	}
	want := "Split the rent by shares\n\nBranch: split-rule\n\nPrompts:\n\n  Split the rent\n  by shares\n\n  Name the rule\n\nSquashed from split-rule into main."
	if got := git(t, r, "log", "-1", "--format=%B", sha); got != want {
		t.Errorf("squash message:\n%s\n--- want ---\n%s", got, want)
	}
	if got := git(t, r, "rev-list", "--count", "main"); got != "2" {
		t.Errorf("main has %s commits, want start plus one squash", got)
	}
	git(t, r, "branch", "-q", "-D", "split-rule")
	recs, err := r.Read("split-rule")
	if err != nil {
		t.Fatal(err)
	}
	m := recs[len(recs)-1]
	if m.Type != ledger.TypeMergeMarker || m.SHA != sha || m.Target != "main" || m.Via != "squash" || m.BranchTip != tip {
		t.Errorf("merge-marker: %+v", m)
	}
	if got := git(t, r, "rev-parse", ledger.Ref("split-rule")+"^2"); got != tip {
		t.Errorf("marker's second parent %s, want the branch tip %s", got, tip)
	}
}
