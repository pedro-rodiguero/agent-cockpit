// Package chat turns an agent's session log into a short, readable conversation for the grid cards.
package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/tailfile"
)

// Kind says who a line comes from.
type Kind int

const (
	User      Kind = iota // the person (or a peer session speaking for someone)
	Assistant             // the agent's prose
	Tool                  // a tool call, summarized as "<tool> <main argument>"
	Note                  // an event in the conversation: interrupted, compacted
)

// Line is one displayable line of a conversation. Text is a single paragraph without newlines.
type Line struct {
	Kind Kind
	Text string
}

// Info is what a log says about the session besides the conversation. Fields are zero when the log does
// not record them.
type Info struct {
	Model         string // the model answering, without a vendor prefix: "opus-5-5", "gpt-6.1"
	Branch        string // git branch of the working directory
	Effort        string // reasoning effort of the last turn
	Mode          string // permission mode when it is not the default: "plan", "bypass", "auto-edit"
	ContextTokens int    // tokens the model was given on its last request
	ContextWindow int    // size of the model's context window, when the log states it
	// Activity is the most recent tool call that has not returned yet ("Bash npm test"), and Running
	// how many such calls there are. Both are zero between turns.
	Activity string
	Running  int
}

// Conversation is the displayable end of a session log.
type Conversation struct {
	Lines []Line
	Info  Info
}

// Reader parses session logs and caches the result per file until the file changes.
// The zero value is ready to use.
type Reader struct {
	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	size, mtime int64
	conv        Conversation
	used        time.Time
}

const (
	tailBytes = 2 << 20          // how much of a log is parsed; large tool results can fill most of it
	keepLines = 400              // lines kept per log, more than any card shows
	forget    = 10 * time.Minute // cached logs not read for this long are dropped
)

// Read returns the last max lines of the conversation in path, and what the log says about the
// session. agent picks the parser.
func (r *Reader) Read(agent, path string, max int) Conversation {
	if path == "" {
		return Conversation{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Conversation{}
	}
	now := time.Now()
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]entry{}
	}
	e, ok := r.cache[path]
	r.mu.Unlock()
	if !ok || e.size != fi.Size() || e.mtime != fi.ModTime().UnixNano() {
		e = entry{size: fi.Size(), mtime: fi.ModTime().UnixNano(), conv: parse(agent, path)}
	}
	e.used = now
	r.mu.Lock()
	r.cache[path] = e
	for p, old := range r.cache {
		if now.Sub(old.used) > forget {
			delete(r.cache, p)
		}
	}
	r.mu.Unlock()
	c := e.conv
	if len(c.Lines) > max {
		c.Lines = c.Lines[len(c.Lines)-max:]
	}
	return c
}

func parse(agent, path string) Conversation {
	b, _, err := tailfile.Read(path, tailBytes)
	if err != nil {
		return Conversation{}
	}
	var c Conversation
	switch agent {
	case "claude":
		c = parseClaude(b)
	case "codex":
		c = parseCodex(b)
	}
	if len(c.Lines) > keepLines {
		c.Lines = c.Lines[len(c.Lines)-keepLines:]
	}
	return c
}

// flat collapses all whitespace, newlines included, into single spaces.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// tilde abbreviates the home directory at the start of a path.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// ---------- Claude Code ----------

type claudeEntry struct {
	Type           string `json:"type"`
	IsMeta         bool   `json:"isMeta"`
	IsSidechain    bool   `json:"isSidechain"` // a subagent's message, not part of the main conversation
	CompactSummary bool   `json:"isCompactSummary"`
	GitBranch      string `json:"gitBranch"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permissionMode"`
	Message        struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Origin *struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"origin"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`          // tool_use
	Name      string          `json:"name"`        // tool_use
	Input     json.RawMessage `json:"input"`       // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
}

// pendingCalls tracks tool calls that have started and not returned, in the order they started.
type pendingCalls struct {
	ids   []string
	label map[string]string
}

func (p *pendingCalls) start(id, label string) {
	if p.label == nil {
		p.label = map[string]string{}
	}
	p.ids = append(p.ids, id)
	p.label[id] = label
}

func (p *pendingCalls) finish(id string) { delete(p.label, id) }

// clear forgets every call: a new prompt or an interruption means none of them is still running.
func (p *pendingCalls) clear() { p.ids, p.label = nil, nil }

// report fills Activity with the latest call still running and Running with how many there are.
func (p *pendingCalls) report(info *Info) {
	for _, id := range p.ids {
		if l, ok := p.label[id]; ok {
			info.Activity = l
			info.Running++
		}
	}
}

// claudeModes names the permission modes worth showing; the default mode is left out.
var claudeModes = map[string]string{
	"bypassPermissions": "bypass",
	"acceptEdits":       "auto-edit",
	"plan":              "plan",
	"auto":              "auto",
}

// parseClaude reads Claude Code transcript records. Meta records, command echoes ("<command-name>…")
// and tool results are skipped; messages that another session sent over the peer socket are shown with
// the sender's name.
func parseClaude(b []byte) Conversation {
	var out []Line
	var info Info
	var calls pendingCalls
	for _, raw := range bytes.Split(b, []byte("\n")) {
		if bytes.Contains(raw, []byte(`"type":"permission-mode"`)) {
			var e claudeEntry
			if json.Unmarshal(raw, &e) == nil && e.Type == "permission-mode" {
				info.Mode = claudeModes[e.PermissionMode]
			}
			continue
		}
		if !bytes.Contains(raw, []byte(`"type":"user"`)) && !bytes.Contains(raw, []byte(`"type":"assistant"`)) {
			continue
		}
		var e claudeEntry
		if json.Unmarshal(raw, &e) != nil || (e.Type != "user" && e.Type != "assistant") || e.IsSidechain {
			continue
		}
		var text string
		var blocks []block
		if json.Unmarshal(e.Message.Content, &text) != nil {
			_ = json.Unmarshal(e.Message.Content, &blocks)
		}
		if e.GitBranch != "" && e.GitBranch != "HEAD" {
			info.Branch = e.GitBranch
		}
		if e.Type == "assistant" {
			if m := e.Message.Model; m != "" && m != "<synthetic>" {
				info.Model = strings.TrimPrefix(m, "claude-")
			}
			if u := e.Message.Usage; u != nil {
				info.ContextTokens = u.Input + u.CacheCreation + u.CacheRead
			}
			if e.Effort != "" {
				info.Effort = e.Effort
			}
		}
		if e.Type == "user" {
			for _, bl := range blocks {
				if bl.Type == "tool_result" {
					calls.finish(bl.ToolUseID)
				}
			}
			if e.Origin != nil && e.Origin.Kind == "peer" {
				out = append(out, Line{User, "[" + firstNonEmpty(e.Origin.Name, "peer") + "] " + flat(stripEnvelope(text))})
				continue
			}
			if e.CompactSummary {
				out = append(out, Line{Note, "conversation compacted"})
				continue
			}
			if e.IsMeta {
				continue
			}
			for _, bl := range blocks {
				if bl.Type == "text" {
					text += bl.Text
				}
			}
			t := strings.TrimSpace(text)
			if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request interrupted") {
				if strings.HasPrefix(t, "[Request interrupted") {
					calls.clear()
					out = append(out, Line{Note, "interrupted"})
				}
				continue
			}
			calls.clear()
			out = append(out, Line{User, flat(t)})
			continue
		}
		for _, bl := range blocks {
			switch bl.Type {
			case "text":
				if t := strings.TrimSpace(bl.Text); t != "" {
					for _, para := range strings.Split(t, "\n") {
						if p := strings.TrimSpace(para); p != "" {
							out = append(out, Line{Assistant, strings.ReplaceAll(p, "**", "")})
						}
					}
				}
			case "tool_use":
				label := strings.TrimSpace(toolName(bl.Name) + " " + toolArg(bl.Input))
				calls.start(bl.ID, label)
				out = append(out, Line{Tool, label})
			}
		}
	}
	calls.report(&info)
	return Conversation{Lines: out, Info: info}
}

// stripEnvelope removes the <cross-session-message …> wrapper Claude Code puts around peer messages.
func stripEnvelope(s string) string {
	if i := strings.Index(s, ">\n"); strings.HasPrefix(s, "<cross-session-message") && i > 0 {
		s = s[i+2:]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "</cross-session-message>")
}

// toolName shortens MCP tool names: mcp__server__tool → server.tool.
func toolName(n string) string {
	if strings.HasPrefix(n, "mcp__") {
		parts := strings.SplitN(strings.TrimPrefix(n, "mcp__"), "__", 2)
		if len(parts) == 2 {
			return parts[0] + "." + parts[1]
		}
	}
	return n
}

// argKeys are the tool inputs worth showing, most telling first.
var argKeys = []string{"command", "file_path", "path", "pattern", "url", "query", "skill", "description", "prompt", "subagent_type"}

// toolArg picks the one input that best says what a tool call does, falling back to the first
// string input in key order.
func toolArg(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range argKeys {
		if v, ok := m[k].(string); ok && v != "" {
			if k == "file_path" || k == "path" {
				v = tilde(v)
			}
			return flat(v)
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return flat(v)
		}
	}
	return ""
}

// ---------- Codex ----------

type codexEntry struct {
	Type    string `json:"type"`
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"` // turn_context
		Info  *struct {
			Last struct {
				Total int `json:"total_tokens"`
			} `json:"last_token_usage"`
			Window int `json:"model_context_window"`
		} `json:"info"` // token_count
		Item struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Command []string                   `json:"command"`
			Changes map[string]json.RawMessage `json:"changes"`
			Server  string                     `json:"server"`
			Tool    string                     `json:"tool"`
			Path    string                     `json:"path"`
		} `json:"item"`
	} `json:"payload"`
}

// parseCodex reads the item_completed events of a Codex rollout.
func parseCodex(b []byte) Conversation {
	var out []Line
	var info Info
	wanted := [][]byte{[]byte(`"item_completed"`), []byte(`"turn_aborted"`), []byte(`"turn_context"`), []byte(`"token_count"`)}
	for _, raw := range bytes.Split(b, []byte("\n")) {
		if !containsAny(raw, wanted) {
			continue
		}
		var e codexEntry
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		switch {
		case e.Type == "turn_context":
			if e.Payload.Model != "" {
				info.Model = e.Payload.Model
			}
			continue
		case e.Payload.Type == "token_count":
			if ti := e.Payload.Info; ti != nil {
				info.ContextTokens, info.ContextWindow = ti.Last.Total, ti.Window
			}
			continue
		}
		if e.Payload.Type == "turn_aborted" {
			out = append(out, Line{Note, "interrupted"})
			continue
		}
		it := e.Payload.Item
		text := func() string {
			var parts []string
			for _, c := range it.Content {
				parts = append(parts, c.Text)
			}
			return strings.Join(parts, "\n")
		}
		switch it.Type {
		case "UserMessage":
			out = append(out, Line{User, flat(text())})
		case "AgentMessage":
			for _, para := range strings.Split(text(), "\n") {
				if p := strings.TrimSpace(para); p != "" {
					out = append(out, Line{Assistant, strings.ReplaceAll(p, "**", "")})
				}
			}
		case "CommandExecution":
			cmd := it.Command
			if len(cmd) >= 3 && strings.HasSuffix(cmd[0], "sh") && strings.HasPrefix(cmd[1], "-") {
				cmd = cmd[2:]
			}
			out = append(out, Line{Tool, "Bash " + flat(strings.Join(cmd, " "))})
		case "FileChange":
			var files []string
			for f := range it.Changes {
				files = append(files, tilde(f))
			}
			sort.Strings(files)
			out = append(out, Line{Tool, "Edit " + strings.Join(files, ", ")})
		case "McpToolCall":
			out = append(out, Line{Tool, it.Server + "." + it.Tool})
		case "ImageView":
			out = append(out, Line{Tool, "View " + filepath.Base(it.Path)})
		}
	}
	return Conversation{Lines: out, Info: info}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func containsAny(b []byte, subs [][]byte) bool {
	for _, s := range subs {
		if bytes.Contains(b, s) {
			return true
		}
	}
	return false
}
