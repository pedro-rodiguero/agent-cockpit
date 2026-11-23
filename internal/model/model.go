// Package model holds the types every part of cockpit shares: a Session is one row on the dashboard,
// whatever produced it, and a Snapshot is one pass over all sources.
//
// Session is also the wire format of external sources (see docs/sources.md), so its JSON field names
// are a public contract.
package model

import "time"

// State is what a session needs from the person, if anything.
type State string

const (
	Waiting State = "waiting" // the agent asked a question or a permission and is blocked on it
	Stuck   State = "stuck"   // working, but nothing has been written for a long time
	Busy    State = "busy"    // a turn is in progress
	Idle    State = "idle"    // the turn ended; the next move is the person's
	Inbox   State = "inbox"   // not an agent: something waiting to be read or answered elsewhere
)

// Rank orders states by urgency, most urgent first.
func (s State) Rank() int {
	switch s {
	case Waiting:
		return 0
	case Stuck:
		return 1
	case Inbox:
		return 2
	case Busy:
		return 3
	default:
		return 4
	}
}

// NeedsAttention reports whether the session is blocked on the person.
func (s State) NeedsAttention() bool { return s == Waiting || s == Stuck || s == Inbox }

// Session is one agent session or inbox item.
type Session struct {
	ID    string `json:"id"`    // unique across sources, e.g. "claude:<session id>"
	Agent string `json:"agent"` // the tool or source that produced it: claude, codex, agy, ...
	Name  string `json:"name"`  // short label shown on the card
	State State  `json:"state"`

	// Detail says why the state is what it is ("permission prompt", "no activity for 12m").
	Detail string `json:"detail,omitempty"`
	// Title summarizes the conversation or item; LastPrompt is the person's latest message to it.
	Title      string `json:"title,omitempty"`
	LastPrompt string `json:"lastPrompt,omitempty"`

	Since        time.Time `json:"since"`        // when the current state began
	LastActivity time.Time `json:"lastActivity"` // last write to the session's log
	Started      time.Time `json:"started"`

	// Where the session runs. Cwd is an absolute path; TTY is a device name such as "ttys004".
	Cwd string `json:"cwd,omitempty"`
	PID int    `json:"pid,omitempty"`
	TTY string `json:"tty,omitempty"`

	// URL is opened when an item without a terminal is activated.
	URL string `json:"url,omitempty"`
	// Log is the transcript the chat card reads (a Claude or Codex .jsonl).
	Log string `json:"log,omitempty"`
	// Heuristic marks a state that was inferred (e.g. from file activity) rather than reported.
	Heuristic bool `json:"heuristic,omitempty"`
	// Quiet items are listed but never raise a desktop notification, for sources whose backend
	// already notifies the person on its own.
	Quiet bool `json:"quiet,omitempty"`

	// Agent-specific handles used to act on the session.
	Profile     string `json:"profile,omitempty"`     // Claude config profile name, "" for the default
	Socket      string `json:"socket,omitempty"`      // Claude peer-messaging socket
	SessionsDir string `json:"sessionsDir,omitempty"` // Claude <config>/sessions directory
	NativeID    string `json:"nativeId,omitempty"`    // the agent's own session id, used to resume it
}

// Snapshot is the result of one collection pass.
type Snapshot struct {
	At       time.Time         `json:"at"`
	Sessions []Session         `json:"sessions"`
	Errors   map[string]string `json:"errors,omitempty"` // source name → last error
}

// Count returns how many sessions are in state st.
func (s Snapshot) Count(st State) int {
	n := 0
	for _, x := range s.Sessions {
		if x.State == st {
			n++
		}
	}
	return n
}
