// Package config reads cockpit's settings file, <user config dir>/agent-cockpit/config.json. cockpit
// only reads it; the menu bar app's Preferences window and the person write it. Every field is
// optional, and a missing file is the defaults. docs/configuration.md lists the fields.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// Themes.
const (
	Dark   = "dark"
	Light  = "light"
	System = "system" // follow the system's appearance (macOS); dark elsewhere
)

// Config is the content of the file.
type Config struct {
	Theme string `json:"theme,omitempty"`
	// Background "terminal" leaves the page background to the terminal, so its own background (and
	// its transparency) shows through; the default paints the theme's.
	Background string   `json:"background,omitempty"`
	QuietAfter Duration `json:"quietAfter,omitzero"` // idle this long, a session moves to the rail
	StuckAfter Duration `json:"stuckAfter,omitzero"` // silent this long while working, it counts as stuck
	Accounts   Accounts `json:"accounts,omitzero"`
	Sources    Sources  `json:"sources,omitzero"`
	// Todo holds the to-do list's settings. They live here, not in the list, because they belong to
	// this machine (an office address) while the list is meant to be shared between devices.
	Todo todo.Settings `json:"todo,omitzero"`
	App  App           `json:"app,omitzero"`
}

// Accounts are where each agent keeps its sessions. Empty fields mean the agent's defaults.
type Accounts struct {
	Claude []string `json:"claude,omitempty"` // Claude Code config dirs, default profile first
	Codex  string   `json:"codex,omitempty"`  // CODEX_HOME
	Agy    string   `json:"agy,omitempty"`    // the antigravity-cli folder
}

// Sources picks the sources to collect from.
type Sources struct {
	Off []string `json:"off,omitempty"` // names of sources not to collect from
}

// App holds the menu bar app's settings. cockpit reads only Opacity, to know when its window lets the
// desktop show through.
type App struct {
	Opacity float64 `json:"opacity,omitempty"` // of the dashboard window's background, 0–1; 1 when unset
}

// Duration is a time.Duration written as a string ("10m", "1h30m").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if v <= 0 {
		return fmt.Errorf("duration %q is not positive", s)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// Or is d, or def when d is unset.
func (d Duration) Or(def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return time.Duration(d)
}

// Off reports whether the source named name is turned off.
func (c Config) Off(name string) bool {
	return slices.Contains(c.Sources.Off, name)
}

// TerminalBackground reports whether the page background is left to the terminal: when asked for, or
// inside the app's window while it is translucent.
func (c Config) TerminalBackground(termProgram string) bool {
	if c.Background == "terminal" {
		return true
	}
	return termProgram == "agent-cockpit" && c.App.Opacity > 0 && c.App.Opacity < 1
}

// DefaultPath is <user config dir>/agent-cockpit/config.json, next to the to-do list.
func DefaultPath() string { return filepath.Join(filepath.Dir(todo.DefaultPath()), "config.json") }

// Load reads the file at path. A missing file is the zero Config.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.Accounts.Codex = Expand(c.Accounts.Codex)
	c.Accounts.Agy = Expand(c.Accounts.Agy)
	for i, d := range c.Accounts.Claude {
		c.Accounts.Claude[i] = Expand(d)
	}
	return c, nil
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// File is the settings file, read again whenever it changes. Its methods are safe for concurrent use,
// and a nil File is the defaults.
type File struct {
	Path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	cur     Config
	err     error
}

// Get returns the current settings. While the file does not parse, it keeps the last good settings and
// returns the error with them.
func (f *File) Get() (Config, error) {
	if f == nil {
		return Config{}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, err := os.Stat(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		f.modTime, f.size, f.cur, f.err = time.Time{}, 0, Config{}, nil
		return f.cur, nil
	}
	if err != nil {
		return f.cur, err
	}
	if fi.ModTime().Equal(f.modTime) && fi.Size() == f.size {
		return f.cur, f.err
	}
	c, err := Load(f.Path)
	f.modTime, f.size, f.err = fi.ModTime(), fi.Size(), err
	if err == nil {
		f.cur = c
	}
	return f.cur, f.err
}

// Current returns the settings, ignoring a file that does not parse (the last good settings stand).
func (f *File) Current() Config {
	c, _ := f.Get()
	return c
}
