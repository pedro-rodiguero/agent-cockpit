package source

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Default builds the collector from the environment:
//
//	COCKPIT_STUCK_AFTER   duration before a silent busy session counts as stuck (default 10m)
//	COCKPIT_CLAUDE_DIRS   colon-separated Claude Code config dirs (see ClaudeDirs)
//	COCKPIT_SOURCES       comma-separated source names to enable (default: all)
func Default() *Collector {
	home, _ := os.UserHomeDir()
	stuck := 10 * time.Minute
	if v := os.Getenv("COCKPIT_STUCK_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			stuck = d
		}
	}
	all := []Source{
		&Claude{Dirs: ClaudeDirs()},
		&Codex{Root: filepath.Join(home, ".codex", "sessions")},
		&Agy{Root: filepath.Join(home, ".gemini", "antigravity-cli")},
		&Tasks{Store: &todo.Store{Path: todo.DefaultPath()}},
	}
	for _, e := range FindExternal(os.Getenv("PATH")) {
		all = append(all, e)
	}
	if v := os.Getenv("COCKPIT_SOURCES"); v != "" {
		want := map[string]bool{}
		for _, n := range strings.Split(v, ",") {
			want[strings.TrimSpace(n)] = true
		}
		var picked []Source
		for _, s := range all {
			if want[s.Name()] {
				picked = append(picked, s)
			}
		}
		all = picked
	}
	return &Collector{Sources: all, StuckAfter: stuck}
}

// ClaudeDirs lists the Claude Code config dirs to watch, default profile first. COCKPIT_CLAUDE_DIRS
// overrides it; otherwise it is $CLAUDE_CONFIG_DIR (or ~/.claude) followed by every ~/.claude-<name>
// directory that holds a sessions/ folder, which is where extra profiles usually live.
func ClaudeDirs() []string {
	if v := os.Getenv("COCKPIT_CLAUDE_DIRS"); v != "" {
		return filepath.SplitList(v)
	}
	home, _ := os.UserHomeDir()
	first := filepath.Join(home, ".claude")
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		first = v
	}
	dirs := []string{first}
	extra, _ := filepath.Glob(filepath.Join(home, ".claude-*", "sessions"))
	for _, d := range extra {
		if d = filepath.Dir(d); d != first {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
