package source

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/tailfile"
)

// Claude reads the <dir>/sessions/<pid>.json file that every interactive Claude Code process keeps
// current (status, waitingFor, messaging socket), and the session transcript for its generated title
// and the last prompt.
type Claude struct {
	// Dirs are Claude Code config dirs; the first is the default profile.
	Dirs []string

	mu    sync.Mutex
	cache map[string]transcriptMeta // keyed by dir|sessionID
}

type transcriptMeta struct {
	path        string
	size, mtime int64
	title       string
	prompt      string
	fullScanned bool // the whole file was searched once; later updates only read the tail
}

// metaTail is how much of a transcript is searched for its title and last prompt on each change.
const metaTail = 2 << 20

type claudeRecord struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	StartedAt       int64  `json:"startedAt"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	UpdatedAt       int64  `json:"updatedAt"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	WaitingFor      string `json:"waitingFor"`
	Socket          string `json:"messagingSocketPath"`
}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Collect(ctx context.Context, env *Env) ([]model.Session, error) {
	var out []model.Session
	seen := map[string]bool{}
	for i, dir := range c.Dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		profile := ""
		if i > 0 {
			profile = strings.TrimPrefix(strings.TrimPrefix(filepath.Base(dir), ".claude"), "-")
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var r claudeRecord
			if json.Unmarshal(b, &r) != nil || r.PID == 0 {
				continue
			}
			// The file outlives a crashed process, and pids get reused.
			p, alive := env.Procs[r.PID]
			if !alive || !strings.Contains(p.Args, "claude") {
				continue
			}
			seen[dir+"|"+r.SessionID] = true
			s := model.Session{
				ID:      "claude:" + r.SessionID,
				Agent:   "claude",
				Name:    r.Name,
				Profile: profile,
				Cwd:     r.Cwd,
				PID:     r.PID,
				TTY:     p.TTY,
				Detail:  r.WaitingFor,
				Since:   msTime(r.StatusUpdatedAt),
				Started: msTime(r.StartedAt),

				Socket:      r.Socket,
				SessionsDir: filepath.Join(dir, "sessions"),
				NativeID:    r.SessionID,
			}
			if r.Kind != "" && r.Kind != "interactive" {
				s.Detail = strings.TrimSpace(r.Kind + " " + s.Detail)
			}
			switch r.Status {
			case "waiting":
				s.State = model.Waiting
			case "busy":
				s.State = model.Busy
			default:
				s.State = model.Idle
			}
			m := c.transcript(dir, r.SessionID)
			s.Title, s.LastPrompt, s.Log = m.title, m.prompt, m.path
			s.LastActivity = msTime(r.UpdatedAt)
			if m.mtime > 0 && time.Unix(0, m.mtime).After(s.LastActivity) {
				s.LastActivity = time.Unix(0, m.mtime)
			}
			out = append(out, s)
		}
	}
	c.prune(seen)
	return out, nil
}

func (c *Claude) prune(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.cache {
		if !seen[k] {
			delete(c.cache, k)
		}
	}
}

// transcript returns the title and last prompt of a session, re-reading the transcript only when it
// changed. Both lines are rewritten as the conversation goes on, so the newest copy is near the end; a
// session whose title was written long ago gets one full scan and is then kept up to date from the tail.
func (c *Claude) transcript(dir, sid string) transcriptMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string]transcriptMeta{}
	}
	key := dir + "|" + sid
	m := c.cache[key]
	if m.path == "" {
		matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sid+".jsonl"))
		if len(matches) == 0 {
			return m
		}
		m.path = matches[0]
	}
	fi, err := os.Stat(m.path)
	if err != nil {
		return m
	}
	if fi.Size() == m.size && fi.ModTime().UnixNano() == m.mtime {
		return m
	}
	title, prompt := scanClaudeMeta(m.path, metaTail)
	if (title == "" || prompt == "") && !m.fullScanned && fi.Size() > metaTail {
		m.fullScanned = true
		t2, p2 := scanClaudeMeta(m.path, fi.Size())
		if title == "" {
			title = t2
		}
		if prompt == "" {
			prompt = p2
		}
	}
	if title != "" {
		m.title = title
	}
	if prompt != "" {
		m.prompt = prompt
	}
	m.size, m.mtime = fi.Size(), fi.ModTime().UnixNano()
	c.cache[key] = m
	return m
}

// scanClaudeMeta finds the newest ai-title and last-prompt records in the last tail bytes of path.
func scanClaudeMeta(path string, tail int64) (title, prompt string) {
	b, _, err := tailfile.Read(path, tail)
	if err != nil {
		return
	}
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0 && (title == "" || prompt == ""); i-- {
		l := lines[i]
		if title == "" && bytes.Contains(l, []byte(`"ai-title"`)) {
			var v struct {
				AITitle string `json:"aiTitle"`
			}
			if json.Unmarshal(l, &v) == nil && v.AITitle != "" {
				title = v.AITitle
			}
		}
		if prompt == "" && bytes.Contains(l, []byte(`"lastPrompt"`)) {
			var v struct {
				LastPrompt string `json:"lastPrompt"`
			}
			if json.Unmarshal(l, &v) == nil && v.LastPrompt != "" {
				prompt = clip(v.LastPrompt, 400)
			}
		}
	}
	return
}
