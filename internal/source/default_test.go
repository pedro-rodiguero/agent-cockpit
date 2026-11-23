package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COCKPIT_CLAUDE_DIRS", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, d := range []string{".claude-work/sessions", ".claude-empty"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(ClaudeDirs(), " ")
	want := filepath.Join(home, ".claude") + " " + filepath.Join(home, ".claude-work")
	if got != want {
		t.Errorf("default: %s, want %s", got, want)
	}
	t.Setenv("COCKPIT_CLAUDE_DIRS", "/a:/b")
	if got := strings.Join(ClaudeDirs(), " "); got != "/a /b" {
		t.Errorf("override: %s", got)
	}
}
