package source

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// DefaultStuckAfter is how long a working session may stay silent before it counts as stuck.
const DefaultStuckAfter = 10 * time.Minute

// Default builds the collector from the settings file and the environment, which wins over it:
//
//	COCKPIT_STUCK_AFTER   duration before a silent busy session counts as stuck
//	COCKPIT_CLAUDE_DIRS   colon-separated Claude Code config dirs (see ClaudeDirs)
//	COCKPIT_SOURCES       comma-separated source names to enable (default: all but the ones turned off)
//
// The to-do list's settings are read from the file on every pass, so they apply without a restart.
func Default(file *config.File) *Collector {
	cfg := file.Current()
	stuck := cfg.StuckAfter.Or(DefaultStuckAfter)
	if v := os.Getenv("COCKPIT_STUCK_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			stuck = d
		}
	}
	all := Available(cfg)
	pick := func(s Source) bool { return !cfg.Off(s.Name()) }
	if v := os.Getenv("COCKPIT_SOURCES"); v != "" {
		want := map[string]bool{}
		for _, n := range strings.Split(v, ",") {
			want[strings.TrimSpace(n)] = true
		}
		pick = func(s Source) bool { return want[s.Name()] }
	}
	var picked []Source
	for _, s := range all {
		if pick(s) {
			if t, ok := s.(*Tasks); ok {
				t.Store.Settings = func() todo.Settings { return file.Current().Todo }
			}
			picked = append(picked, s)
		}
	}
	return &Collector{Sources: picked, StuckAfter: stuck}
}

// Available lists every source cockpit can collect from, turned off or not: the built-in ones and the
// external sources on PATH.
func Available(cfg config.Config) []Source {
	all := []Source{
		&Claude{Dirs: ClaudeDirs(cfg)},
		&Codex{Root: filepath.Join(CodexHome(cfg), "sessions")},
		&Agy{Root: AgyRoot(cfg)},
		&Tasks{Store: &todo.Store{Path: todo.DefaultPath()}},
	}
	for _, e := range FindExternal(os.Getenv("PATH")) {
		all = append(all, e)
	}
	return all
}

// ClaudeDirs lists the Claude Code config dirs to watch, default profile first: COCKPIT_CLAUDE_DIRS,
// else the settings file's accounts.claude, else DetectClaudeDirs.
func ClaudeDirs(cfg config.Config) []string {
	if v := os.Getenv("COCKPIT_CLAUDE_DIRS"); v != "" {
		return filepath.SplitList(v)
	}
	if len(cfg.Accounts.Claude) > 0 {
		return cfg.Accounts.Claude
	}
	return DetectClaudeDirs()
}

// DetectClaudeDirs is $CLAUDE_CONFIG_DIR (or ~/.claude) followed by every ~/.claude-<name> directory
// that holds a sessions/ folder, which is where extra profiles usually live.
func DetectClaudeDirs() []string {
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

// CodexHome is Codex's folder: accounts.codex, else $CODEX_HOME, else ~/.codex.
func CodexHome(cfg config.Config) string {
	if cfg.Accounts.Codex != "" {
		return cfg.Accounts.Codex
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// AgyRoot is agy's folder: accounts.agy, else ~/.gemini/antigravity-cli.
func AgyRoot(cfg config.Config) string {
	if cfg.Accounts.Agy != "" {
		return cfg.Accounts.Agy
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli")
}
