package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
)

// launcher is one entry of the agent picker in the new-pane prompt.
type launcher struct {
	label   string
	agent   string
	profile string // Claude config dir for a non-default profile
}

// launchers lists what a new pane can run: Claude Code once per profile in dirs (the Claude config
// dirs cockpit watches), then the other agents.
func launchers(dirs []string) []launcher {
	var l []launcher
	for _, d := range dirs {
		if isDefaultProfile(d) {
			l = append(l, launcher{label: "claude", agent: "claude"})
		} else if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			name := strings.TrimPrefix(strings.TrimPrefix(filepath.Base(d), ".claude"), "-")
			l = append(l, launcher{label: "claude " + name, agent: "claude", profile: d})
		}
	}
	if len(l) == 0 {
		l = append(l, launcher{label: "claude", agent: "claude"})
	}
	return append(l, launcher{label: "codex", agent: "codex"}, launcher{label: "agy", agent: "agy"}, launcher{label: "shell", agent: "shell"})
}

// isDefaultProfile reports whether dir is the config dir Claude Code uses when started without one:
// $CLAUDE_CONFIG_DIR or ~/.claude, or the first of COCKPIT_CLAUDE_DIRS when that is set.
func isDefaultProfile(dir string) bool {
	def := source.DetectClaudeDirs()[0]
	if v := filepath.SplitList(os.Getenv("COCKPIT_CLAUDE_DIRS")); len(v) > 0 {
		def = v[0]
	}
	return dir == "" || filepath.Clean(dir) == filepath.Clean(def)
}

// profileEnv selects a Claude profile the way a `claude2`-style shell function does: the config dir, plus
// the OAuth token kept in <dir>/token when there is one. The default profile needs nothing.
func profileEnv(dir string) []string {
	if isDefaultProfile(dir) {
		return nil
	}
	env := []string{"CLAUDE_CONFIG_DIR=" + dir}
	if b, err := os.ReadFile(filepath.Join(dir, "token")); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
		}
	}
	return env
}
