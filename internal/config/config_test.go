package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home folder on Windows
	path := filepath.Join(t.TempDir(), "config.json")
	if c, err := Load(path); err != nil || c.Theme != "" {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	write(t, path, `{"theme":"light","quietAfter":"30m","accounts":{"claude":["~/.claude-work"],"codex":"/opt/codex"},
		"sources":{"off":["agy"]},"todo":{"defaultGroup":"Home"},"app":{"opacity":0.8,"blur":true}}`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Theme != Light || c.QuietAfter.Or(time.Hour) != 30*time.Minute || c.StuckAfter.Or(time.Minute) != time.Minute {
		t.Errorf("fields: %+v", c)
	}
	if c.Accounts.Claude[0] != filepath.Join(home, ".claude-work") || c.Accounts.Codex != "/opt/codex" {
		t.Errorf("accounts: %+v", c.Accounts)
	}
	if !c.Off("agy") || c.Off("codex") || c.Todo.DefaultGroup != "Home" {
		t.Errorf("sources or todo: %+v", c)
	}
	for _, bad := range []string{`{"quietAfter":"soon"}`, `{"quietAfter":"-1m"}`, `{`} {
		write(t, path, bad)
		if _, err := Load(path); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestTerminalBackground(t *testing.T) {
	cases := []struct {
		c    Config
		term string
		want bool
	}{
		{Config{}, "agent-cockpit", false},
		{Config{Background: "terminal"}, "Apple_Terminal", true},
		{Config{App: App{Opacity: 0.85}}, "agent-cockpit", true},
		{Config{App: App{Opacity: 0.85}}, "Apple_Terminal", false}, // the app's opacity is the app's
		{Config{App: App{Opacity: 1}}, "agent-cockpit", false},
	}
	for _, tc := range cases {
		if got := tc.c.TerminalBackground(tc.term); got != tc.want {
			t.Errorf("%+v in %s = %v", tc.c, tc.term, got)
		}
	}
}

func TestFileReloadsAndKeepsLastGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	f := &File{Path: path}
	if c := f.Current(); c.Theme != "" {
		t.Fatalf("missing file: %+v", c)
	}
	write(t, path, `{"theme":"light"}`)
	if c := f.Current(); c.Theme != Light {
		t.Fatalf("after write: %+v", c)
	}
	write(t, path, `{"theme":`)
	if c, err := f.Get(); err == nil || c.Theme != Light {
		t.Errorf("broken file: %+v %v, want the last good settings and an error", c, err)
	}
	write(t, path, `{"theme":"system"}`)
	if c := f.Current(); c.Theme != System {
		t.Errorf("fixed file: %+v", c)
	}
	var none *File
	if c := none.Current(); c.Theme != "" {
		t.Errorf("nil file: %+v", c)
	}
}

// write replaces the file and moves its modification time forward, so a File sees the change even
// when two writes land in the same clock tick.
func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Duration(len(s)) * time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}
