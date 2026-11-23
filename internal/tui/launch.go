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

// launchers lists what a new pane can run: Claude Code once per profile, then the other agents.
func launchers() []launcher {
	l := []launcher{{label: "claude", agent: "claude"}}
	for _, d := range source.ClaudeDirs()[1:] {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			name := strings.TrimPrefix(strings.TrimPrefix(filepath.Base(d), ".claude"), "-")
			l = append(l, launcher{label: "claude " + name, agent: "claude", profile: d})
		}
	}
	return append(l, launcher{label: "codex", agent: "codex"}, launcher{label: "agy", agent: "agy"}, launcher{label: "shell", agent: "shell"})
}

// profileEnv selects a Claude profile the way a `claude2`-style shell function does: the config dir, plus
// the OAuth token kept in <dir>/token when there is one. The default profile needs nothing.
func profileEnv(dir string) []string {
	dirs := source.ClaudeDirs()
	if dir == "" || filepath.Clean(dir) == filepath.Clean(dirs[0]) {
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
