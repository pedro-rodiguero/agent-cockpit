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

// Reader parses session logs and caches the result per file until the file changes.
// The zero value is ready to use.
type Reader struct {
	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	size, mtime int64
	lines       []Line
	used        time.Time
}

const (
	tailBytes = 768 << 10        // how much of a log is parsed; plenty for the visible end of a chat
	keepLines = 400              // lines kept per log, more than any card shows
	forget    = 10 * time.Minute // cached logs not read for this long are dropped
)

// Read returns the last max lines of the conversation in path. agent picks the parser.
func (r *Reader) Read(agent, path string, max int) []Line {
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	now := time.Now()
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]entry{}
	}
	e, ok := r.cache[path]
	r.mu.Unlock()
	if !ok || e.size != fi.Size() || e.mtime != fi.ModTime().UnixNano() {
		e = entry{size: fi.Size(), mtime: fi.ModTime().UnixNano(), lines: parse(agent, path)}
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
	if len(e.lines) > max {
		return e.lines[len(e.lines)-max:]
	}
	return e.lines
}

func parse(agent, path string) []Line {
	b, _, err := tailfile.Read(path, tailBytes)
	if err != nil {
		return nil
	}
	var lines []Line
	switch agent {
	case "claude":
		lines = parseClaude(b)
	case "codex":
		lines = parseCodex(b)
	}
	if len(lines) > keepLines {
		lines = lines[len(lines)-keepLines:]
	}
	return lines
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
	CompactSummary bool   `json:"isCompactSummary"`
	Message        struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Origin *struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"origin"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// parseClaude reads Claude Code transcript records. Meta records, command echoes ("<command-name>…")
// and tool results are skipped; messages that another session sent over the peer socket are shown with
// the sender's name.
func parseClaude(b []byte) []Line {
	var out []Line
	for _, raw := range bytes.Split(b, []byte("\n")) {
		if !bytes.Contains(raw, []byte(`"type":"user"`)) && !bytes.Contains(raw, []byte(`"type":"assistant"`)) {
			continue
		}
		var e claudeEntry
		if json.Unmarshal(raw, &e) != nil || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		var text string
		var blocks []block
		if json.Unmarshal(e.Message.Content, &text) != nil {
			_ = json.Unmarshal(e.Message.Content, &blocks)
		}
		if e.Type == "user" {
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
					out = append(out, Line{Note, "interrupted"})
				}
				continue
			}
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
				out = append(out, Line{Tool, toolName(bl.Name) + " " + toolArg(bl.Input)})
			}
		}
	}
	return out
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
		Type string `json:"type"`
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
func parseCodex(b []byte) []Line {
	var out []Line
	for _, raw := range bytes.Split(b, []byte("\n")) {
		if !bytes.Contains(raw, []byte(`"item_completed"`)) && !bytes.Contains(raw, []byte(`"turn_aborted"`)) {
			continue
		}
		var e codexEntry
		if json.Unmarshal(raw, &e) != nil {
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
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
