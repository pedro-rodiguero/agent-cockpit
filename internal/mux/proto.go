// Package mux is cockpit's terminal multiplexer. `cockpit serve` owns one PTY plus a VT emulator per
// pane, so agents keep running when the dashboard closes; dashboards talk to it over a Unix socket with
// newline-delimited JSON. docs/protocol.md describes the wire format.
package mux

import (
	"os"
	"path/filepath"
	"time"
)

// Operations. Dashboards send attach, spawn, input, paste, resize and close; the server sends panes,
// screen, spawned and error. Anyone may send select, which the server passes on to every dashboard.
const (
	OpAttach = "attach" // subscribe to updates; the server answers with panes and every screen
	OpSpawn  = "spawn"  // start Agent in Cwd with Args and extra Env, sized Cols×Rows
	OpInput  = "input"  // write Data to pane ID's terminal
	OpPaste  = "paste"  // paste Data into pane ID, bracketed if the program enabled bracketed paste
	OpResize = "resize" // resize pane ID to Cols×Rows
	OpClose  = "close"  // stop pane ID's program and drop the pane
	OpSelect = "select" // show session ID: passed on to every attached dashboard

	OpPanes   = "panes"   // the full pane list, sent whenever it changes
	OpScreen  = "screen"  // one pane's visible screen, sent whenever it changes
	OpSpawned = "spawned" // reply to spawn: the new pane's ID
	OpError   = "error"   // reply to a request that failed
)

// PaneInfo describes one pane.
type PaneInfo struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	Cwd      string    `json:"cwd"`
	PID      int       `json:"pid"`
	Title    string    `json:"title,omitempty"` // last title the program set (OSC 0/2)
	Cols     int       `json:"cols"`
	Rows     int       `json:"rows"`
	Started  time.Time `json:"started"`
	Exited   bool      `json:"exited,omitempty"`
	ExitCode int       `json:"exitCode,omitempty"`
	Bell     time.Time `json:"bell,omitempty"` // last BEL
}

// Screen is a pane's visible screen: one string per row, each exactly Cols cells wide, with SGR escape
// sequences already resolved against cockpit's palette.
type Screen struct {
	ID      string   `json:"id"`
	Lines   []string `json:"lines"`
	CX      int      `json:"cx"`
	CY      int      `json:"cy"`
	Cursor  bool     `json:"cursor"` // the program wants the cursor visible
	AltMode bool     `json:"alt,omitempty"`
}

// Msg is every frame on the wire, in both directions. Op says which fields are set.
type Msg struct {
	Op     string     `json:"op"`
	ID     string     `json:"id,omitempty"`
	Agent  string     `json:"agent,omitempty"`
	Cwd    string     `json:"cwd,omitempty"`
	Args   []string   `json:"args,omitempty"`
	Env    []string   `json:"env,omitempty"` // KEY=value pairs added to the agent's environment
	Cols   int        `json:"cols,omitempty"`
	Rows   int        `json:"rows,omitempty"`
	Data   []byte     `json:"data,omitempty"`
	Panes  []PaneInfo `json:"panes,omitempty"`
	Screen *Screen    `json:"screen,omitempty"`
	Err    string     `json:"err,omitempty"`
}

// Dir is where the server keeps its socket and log.
func Dir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "agent-cockpit")
	}
	return filepath.Join(os.TempDir(), "agent-cockpit")
}

// SocketPath is the server's Unix socket.
func SocketPath() string { return filepath.Join(Dir(), "mux.sock") }

// Agents maps the agent names a spawn accepts to the command each one runs. "shell" is the user's
// login shell, or Args when given.
var Agents = map[string][]string{
	"claude": {"claude"},
	"codex":  {"codex"},
	"agy":    {"agy"},
	"shell":  {},
}
