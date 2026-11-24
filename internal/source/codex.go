package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/tailfile"
)

// Codex finds live `codex` processes and reads the rollout .jsonl each one is writing. The state comes
// from the last turn event: task_started → busy; task_complete or turn_aborted → idle; an approval,
// request_user_input or elicitation event after the last task_started → waiting.
type Codex struct {
	Root string // ~/.codex/sessions

	mu    sync.Mutex
	cache map[string]codexState // keyed by rollout path
}

// codexState is what a rollout says about its session, cached until the file changes.
type codexState struct {
	size, mtime  int64
	state        model.State
	since        time.Time
	detail       string
	title        string
	lastPrompt   string
	lastActivity time.Time
}

// codexTail is how much of a rollout is read; one turn's events fit comfortably.
const codexTail = 4 << 20

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Collect(ctx context.Context, env *Env) ([]model.Session, error) {
	var out []model.Session
	used := map[string]bool{}
	for _, p := range env.Procs {
		if p.Cmd != "codex" || p.TTY == "" {
			continue
		}
		if parent, ok := env.Procs[p.PPID]; ok && parent.Cmd == "codex" {
			continue
		}
		cwd := procCwd(ctx, p.PID)
		rollout := ""
		for _, n := range openFiles(ctx, p.PID) {
			if strings.HasSuffix(n, ".jsonl") && strings.HasPrefix(n, c.Root) {
				rollout = n
			}
		}
		if rollout == "" {
			rollout = c.newestFor(cwd, used)
		}
		used[rollout] = true
		s := model.Session{
			ID:    "codex:" + strconv.Itoa(p.PID),
			Agent: "codex",
			Name:  filepath.Base(cwd),
			Cwd:   cwd,
			PID:   p.PID,
			TTY:   p.TTY,
			State: model.Idle,
			Log:   rollout,
		}
		if rollout != "" {
			s.NativeID = rolloutID(rollout)
			st := c.read(rollout)
			s.State, s.Since, s.Detail = st.state, st.since, st.detail
			s.Title, s.LastPrompt, s.LastActivity = st.title, st.lastPrompt, st.lastActivity
		}
		out = append(out, s)
	}
	c.prune(used)
	return out, nil
}

func (c *Codex) prune(live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.cache {
		if !live[k] {
			delete(c.cache, k)
		}
	}
}

// read returns the state recorded in a rollout, parsing it again only when it changed.
func (c *Codex) read(path string) codexState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string]codexState{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return codexState{state: model.Idle}
	}
	if st, ok := c.cache[path]; ok && st.size == fi.Size() && st.mtime == fi.ModTime().UnixNano() {
		return st
	}
	st := parseRollout(path)
	st.size, st.mtime = fi.Size(), fi.ModTime().UnixNano()
	c.cache[path] = st
	return st
}

// newestFor is the fallback when the process does not hold its rollout open: the most recently
// modified rollout of the last two days whose session_meta names the same working directory.
func (c *Codex) newestFor(cwd string, used map[string]bool) string {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	cutoff := time.Now().Add(-48 * time.Hour)
	_ = filepath.WalkDir(c.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(cutoff) {
			files = append(files, file{path, fi.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, ff := range files {
		f := ff.path
		if used[f] {
			continue
		}
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		r := bufio.NewReaderSize(fh, 1<<16)
		first, _ := r.ReadBytes('\n')
		fh.Close()
		var meta struct {
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(first, &meta) == nil && meta.Payload.Cwd == cwd {
			return f
		}
	}
	return ""
}

type codexLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
		LastAgentMessage string `json:"last_agent_message"`
	} `json:"payload"`
}

// parseRollout replays the event_msg records in the tail of a rollout.
func parseRollout(path string) codexState {
	s := codexState{state: model.Idle}
	b, fi, err := tailfile.Read(path, codexTail)
	if err != nil {
		return s
	}
	s.lastActivity = fi.ModTime()
	for _, l := range bytes.Split(b, []byte("\n")) {
		if !bytes.Contains(l, []byte(`"event_msg"`)) {
			continue
		}
		var x codexLine
		if json.Unmarshal(l, &x) != nil {
			continue
		}
		t := x.Payload.Type
		switch {
		case t == "task_started":
			s.state, s.since, s.detail = model.Busy, x.Timestamp, ""
		case t == "task_complete":
			s.state, s.since, s.detail = model.Idle, x.Timestamp, ""
			if x.Payload.LastAgentMessage != "" {
				s.title = clip(x.Payload.LastAgentMessage, 120)
			}
		case t == "turn_aborted":
			s.state, s.since, s.detail = model.Idle, x.Timestamp, "interrupted"
		case strings.Contains(t, "approval") || strings.Contains(t, "request_user_input") || strings.Contains(t, "elicitation"):
			s.state, s.since, s.detail = model.Waiting, x.Timestamp, strings.ReplaceAll(t, "_", " ")
		case t == "item_completed" && x.Payload.Item.Type == "UserMessage":
			var parts []string
			for _, ct := range x.Payload.Item.Content {
				parts = append(parts, ct.Text)
			}
			s.lastPrompt = clip(strings.Join(parts, " "), 400)
		}
	}
	return s
}

// rolloutID takes the session UUID from rollout-<timestamp>-<uuid>.jsonl.
func rolloutID(path string) string {
	b := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if len(b) < 36 {
		return ""
	}
	return b[len(b)-36:]
}
