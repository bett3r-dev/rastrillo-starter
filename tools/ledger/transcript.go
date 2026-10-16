package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Batch is the records one flush adds to one branch's ledger.
type Batch struct {
	Branch  string
	Records []Record
}

// The parts of a Claude Code transcript line the ledger reads.
type entry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	SessionID string `json:"sessionId"`
	GitBranch string `json:"gitBranch"`
	IsMeta    bool   `json:"isMeta"`
	Message   struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input     int64 `json:"input_tokens"`
			CacheRead int64 `json:"cache_read_input_tokens"`
			CacheNew  int64 `json:"cache_creation_input_tokens"`
			Output    int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Records turns transcript lines into ledger records, grouped by the
// branch each line was written on, in the order the branches first
// appear. A line whose branch is unknown or a detached HEAD goes to
// fallback; with no fallback it is dropped. Each batch closes with
// one usage record: the tokens of every model call in it, each call
// counted once though the transcript repeats its usage per block.
// author is "claude~<email>", amadan's form for an agent's records.
func Records(lines [][]byte, author, fallback string) ([]Batch, error) {
	var order []string
	batches := map[string]*Batch{}
	// A streamed model call is several lines sharing message.id, its
	// usage growing as it streams: the last line holds the final count,
	// so each call keeps the usage of its latest line.
	type tally struct {
		calls map[string]Tokens
		model string
		ts    string
		sess  string
	}
	usage := map[string]*tally{}

	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("transcript line %d: %w", i+1, err)
		}
		if (e.Type != "user" && e.Type != "assistant") || e.IsMeta {
			continue
		}
		branch := e.GitBranch
		if branch == "" || branch == "HEAD" {
			branch = fallback
		}
		if branch == "" {
			continue
		}
		ts := e.Timestamp
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ts = Stamp(t)
		}
		var recs []Record
		if e.Type == "user" {
			if text := promptText(e.Message.Content); text != "" {
				recs = append(recs, Record{Type: TypePrompt, TS: ts, Origin: "human", Author: author, Session: e.SessionID, Text: text})
			}
		} else {
			var blocks []block
			_ = json.Unmarshal(e.Message.Content, &blocks)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) != "" {
						recs = append(recs, Record{Type: TypeReply, TS: ts, Origin: "llm", Author: author, Session: e.SessionID, Model: e.Message.Model, Text: b.Text})
					}
				case "tool_use":
					var in bytes.Buffer
					if json.Compact(&in, b.Input) != nil {
						in.Write(b.Input)
					}
					recs = append(recs, Record{Type: TypeTool, TS: ts, Origin: "llm", Author: author, Session: e.SessionID, Text: cut(b.Name+" "+in.String(), ToolTextMax)})
				}
			}
			if u := e.Message.Usage; u != nil && e.Message.Model != "<synthetic>" {
				t := usage[branch]
				if t == nil {
					t = &tally{calls: map[string]Tokens{}}
					usage[branch] = t
				}
				id := e.Message.ID
				if id == "" {
					id = fmt.Sprintf("line %d", i+1) // no id to share: a call of its own
				}
				t.calls[id] = Tokens{In: u.Input, CacheRead: u.CacheRead, CacheNew: u.CacheNew, Out: u.Output}
				t.model, t.ts, t.sess = e.Message.Model, ts, e.SessionID
			}
		}
		if len(recs) == 0 {
			continue
		}
		b := batches[branch]
		if b == nil {
			b = &Batch{Branch: branch}
			batches[branch] = b
			order = append(order, branch)
		}
		b.Records = append(b.Records, recs...)
	}

	out := make([]Batch, 0, len(order))
	for _, br := range order {
		b := *batches[br]
		if t := usage[br]; t != nil {
			var tok Tokens
			for _, c := range t.calls {
				tok.In += c.In
				tok.CacheRead += c.CacheRead
				tok.CacheNew += c.CacheNew
				tok.Out += c.Out
			}
			b.Records = append(b.Records, Record{Type: TypeUsage, TS: t.ts, Session: t.sess, Model: t.model, Tokens: &tok})
		}
		out = append(out, b)
	}
	return out, nil
}

// promptText is what a person typed: a string, or the text blocks of
// an array. An array of tool results is the agent's own traffic, not a
// prompt, and yields "".
func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "image":
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n\n")
}

// Sync appends to the ledger everything the transcript at path has
// gained since the last Sync of the same session, and records how far
// it read. Only whole lines are read, so a line still being written
// waits for the next Sync.
func (r Repo) Sync(path string) error {
	state, err := r.StateDir()
	if err != nil {
		return err
	}
	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	mark := filepath.Join(state, session+".offset")
	var from int64
	if b, err := os.ReadFile(mark); err == nil {
		from, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if from > int64(len(data)) {
		from = 0 // the transcript was replaced; read it again
	}
	end := bytes.LastIndexByte(data[from:], '\n')
	if end < 0 {
		return nil
	}
	chunk := data[from : from+int64(end)+1]
	batches, err := Records(bytes.Split(chunk, []byte("\n")), "claude~"+r.Email(), r.Branch())
	if err != nil {
		return err
	}
	for _, b := range batches {
		if err := r.Append(b.Branch, b.Records); err != nil {
			return err
		}
	}
	return os.WriteFile(mark, []byte(strconv.FormatInt(from+int64(len(chunk)), 10)), 0o600)
}

// Transcript finds a session's transcript under Claude Code's
// projects directory, or returns "".
func Transcript(session string) string {
	home, err := os.UserHomeDir()
	if err != nil || session == "" {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}
