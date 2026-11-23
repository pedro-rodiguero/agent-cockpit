package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// ExternalPrefix is the name prefix of executables that cockpit runs as sources.
const ExternalPrefix = "cockpit-source-"

// External runs an executable that prints a JSON array of sessions on stdout. It lets anything that is
// not a coding agent (an issue tracker, a review queue, a team inbox) appear in cockpit without living in
// this repository. See docs/sources.md for the contract.
//
// The program runs at most once per Every; between runs, and when a run fails, the last good result is
// reused until it is older than Keep. That keeps a slow or offline backend from stalling the dashboard,
// which collects every couple of seconds.
type External struct {
	Path  string
	Every time.Duration
	Keep  time.Duration

	mu      sync.Mutex
	last    []model.Session
	lastOK  time.Time
	nextRun time.Time
}

// Name is the part of the executable name after ExternalPrefix.
func (e *External) Name() string {
	return strings.TrimPrefix(filepath.Base(e.Path), ExternalPrefix)
}

func (e *External) Collect(ctx context.Context, env *Env) ([]model.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if env.Now.Before(e.nextRun) {
		return e.cached(env.Now), nil
	}
	e.nextRun = env.Now.Add(e.Every)
	got, err := e.run(ctx)
	if err != nil {
		return e.cached(env.Now), err
	}
	e.last, e.lastOK = got, env.Now
	return got, nil
}

func (e *External) cached(now time.Time) []model.Session {
	if now.Sub(e.lastOK) > e.Keep {
		e.last = nil
	}
	return e.last
}

func (e *External) run(ctx context.Context) ([]model.Session, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.Path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	var got []model.Session
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		return nil, fmt.Errorf("bad output: %w", err)
	}
	name := e.Name()
	for i := range got {
		s := &got[i]
		if s.Agent == "" {
			s.Agent = name
		}
		if s.ID == "" {
			s.ID = fmt.Sprintf("%s:%d", name, i)
		} else if !strings.HasPrefix(s.ID, name+":") {
			s.ID = name + ":" + s.ID
		}
		if s.State == "" {
			s.State = model.Inbox
		}
	}
	return got, nil
}

// FindExternal returns one External per cockpit-source-* executable on PATH. When two directories hold
// the same name, the first one on PATH wins, as it would in a shell.
func FindExternal(path string) []*External {
	seen := map[string]bool{}
	var out []*External
	for _, dir := range filepath.SplitList(path) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			name := ent.Name()
			if !strings.HasPrefix(name, ExternalPrefix) || seen[name] {
				continue
			}
			full := filepath.Join(dir, name)
			if fi, err := os.Stat(full); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
				continue
			}
			seen[name] = true
			out = append(out, &External{Path: full, Every: 15 * time.Second, Keep: 10 * time.Minute})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
