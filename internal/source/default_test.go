package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
)

func TestClaudeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	t.Setenv("COCKPIT_CLAUDE_DIRS", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, d := range []string{".claude-work/sessions", ".claude-empty"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(ClaudeDirs(config.Config{}), " ")
	want := filepath.Join(home, ".claude") + " " + filepath.Join(home, ".claude-work")
	if got != want {
		t.Errorf("default: %s, want %s", got, want)
	}
	cfg := config.Config{Accounts: config.Accounts{Claude: []string{"/c"}}}
	if got := strings.Join(ClaudeDirs(cfg), " "); got != "/c" {
		t.Errorf("settings: %s", got)
	}
	t.Setenv("COCKPIT_CLAUDE_DIRS", "/a"+string(os.PathListSeparator)+"/b")
	if got := strings.Join(ClaudeDirs(cfg), " "); got != "/a /b" {
		t.Errorf("override: %s", got)
	}
}
