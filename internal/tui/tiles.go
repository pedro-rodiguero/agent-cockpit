package tui

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
)

// tile is one card: a collected session, a pane, or both when the pane runs the session's process.
type tile struct {
	key    string // stable identity across rebuilds: the session ID, or paneKey(pane ID)
	sess   *model.Session
	pane   *mux.PaneInfo
	parked bool      // a pane moved to the rail, by hand or as a quiet SSH connection (see park.go)
	rested time.Time // for a pane: since when nobody typed into it and it printed nothing
}

// identity names a tile across cockpit restarts, for what the person decided about it: where it goes in
// the grid, how big it is, whether it is set aside.
func (t tile) identity() string {
	if t.pane != nil {
		return paneIdentity(*t.pane)
	}
	return t.key
}

// needsYou reports whether the tile wants the person: such a tile is never kept in the rail by a
// pane's quiet or by being set aside, so a question is never missed.
func (t tile) needsYou() bool { return t.state().NeedsAttention() }

func paneKey(id string) string { return "pane:" + id }

func (t tile) state() model.State {
	if t.sess != nil {
		return t.sess.State
	}
	return model.Idle
}

// isItem reports whether the tile is an inbox item or a to-do task from an external source rather
// than an agent.
func (t tile) isItem() bool {
	return t.pane == nil && t.sess != nil && (t.sess.State == model.Inbox || t.sess.State == model.Todo)
}

// isTask reports whether the tile is a to-do task.
func (t tile) isTask() bool { return t.pane == nil && t.sess != nil && t.sess.State == model.Todo }

// livePane reports whether the tile is a pane whose program is still running.
func (t tile) livePane() bool { return t.pane != nil && !t.pane.Exited }

// canFocusTabs says whether cockpit can bring a session's terminal tab to the front here (macOS).
// A variable rather than term.CanFocus itself, so tests can cover both.
var canFocusTabs = term.CanFocus

// hasTab reports whether the session runs in a terminal tab cockpit can go to.
func (t tile) hasTab() bool {
	return canFocusTabs && t.pane == nil && t.sess != nil && t.sess.TTY != ""
}

// canReply reports whether a message can be sent to the session over Claude Code's peer socket.
func (t tile) canReply() bool {
	return t.sess != nil && t.sess.Agent == "claude" && t.sess.Socket != ""
}

// canAdopt reports whether the session runs in a terminal tab and can be resumed in a pane.
func (t tile) canAdopt() bool { return t.pane == nil && t.sess != nil && resumable(*t.sess) == nil }

// name is what the person calls the session: what it is about when the source knows, since names
// derived from directories ("saas-2f") tell apart nothing that shares a directory. An inbox item keeps
// its own name, the person or thing it is about.
func (t tile) name() string {
	if t.sess != nil && t.sess.Title != "" && !t.isItem() {
		return t.sess.Title
	}
	if t.sess != nil && t.sess.Name != "" {
		return t.sess.Name
	}
	if t.pane != nil {
		return filepath.Base(t.pane.Cwd)
	}
	return "?"
}

func (t tile) agent() string {
	if t.sess != nil {
		return t.sess.Agent
	}
	if t.pane != nil {
		return t.pane.Agent
	}
	return ""
}

// quietAfter is how long a session, or a pane nobody typed into and that printed nothing, may sit idle
// before it gives its card up for a row in the rail.
// The settings change it; only the event loop touches it.
var quietAfter = defaultQuietAfter

const defaultQuietAfter = time.Hour

// place is where a tile goes in the grid view: a card in the grid, or a row in one of the rail's
// sections. The rail holds what is not happening now: to-do tasks, items to read elsewhere, panes whose
// program exited, and sessions nobody has touched for a while.
type place int

const (
	inGrid place = iota
	inTodo
	inInbox
	inEnded
	inParked
	inQuiet
)

func (t tile) place(now time.Time) place {
	switch {
	case t.isTask():
		return inTodo
	case t.isItem():
		return inInbox
	case t.pane != nil && t.pane.Exited:
		return inEnded
	case t.parked && !t.needsYou():
		return inParked
	case t.pane == nil && t.state() == model.Idle && now.Sub(since(t)) > quietAfter:
		return inQuiet
	case t.pane != nil && !t.rested.IsZero() && !t.needsYou() && t.state() != model.Busy && now.Sub(t.rested) > quietAfter:
		return inQuiet
	}
	return inGrid
}

// restingSince is when a tile in the rail's idle section went quiet.
func restingSince(t tile) time.Time {
	if t.pane != nil {
		return t.rested
	}
	return since(t)
}

// placing is what the person decided about where tiles go, beyond their own state.
type placing struct {
	parked map[string]bool      // paneIdentity → in the rail: set aside by hand, or a quiet SSH connection
	woke   map[string]time.Time // pane ID → when the person last brought the pane back or typed into it
	order  map[string]int       // identity → the position the person gave the card in the grid
}

// buildTiles merges collected sessions with the server's panes. A pane whose process is a session's
// process (the agent itself, since the pane's shell execs it) becomes one tile with both.
//
// The list keeps the collector's order, most urgent first. The grid view orders tiles by place: cards
// first (panes, then sessions by start time, so cards keep their places), then the rail's sections,
// each of whose quiet sessions comes most recently active first.
// Cards the person arranged come first in the order they gave them; the others follow.
func buildTiles(sessions []model.Session, panes []mux.PaneInfo, view viewKind, now time.Time, pl placing) []tile {
	byPID := map[int]int{}
	for i, s := range sessions {
		if s.PID != 0 {
			byPID[s.PID] = i
		}
	}
	merged := map[int]bool{}
	tiles := make([]tile, 0, len(sessions)+len(panes))
	for i := range panes {
		t := tile{key: paneKey(panes[i].ID), pane: &panes[i], parked: pl.parked[paneIdentity(panes[i])], rested: quietSince(panes[i])}
		if w := pl.woke[panes[i].ID]; w.After(t.rested) {
			t.rested = w
		}
		if j, ok := byPID[panes[i].PID]; ok {
			t.sess = &sessions[j]
			merged[j] = true
		}
		tiles = append(tiles, t)
	}
	var rest []tile
	for i := range sessions {
		if !merged[i] {
			rest = append(rest, tile{key: sessions[i].ID, sess: &sessions[i]})
		}
	}
	if view == gridView {
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].sess.Started.Before(rest[j].sess.Started) })
	}
	tiles = append(tiles, rest...)
	if view == gridView {
		sort.SliceStable(tiles, func(i, j int) bool {
			pi, pj := tiles[i].place(now), tiles[j].place(now)
			switch {
			case pi != pj:
				return pi < pj
			case pi == inQuiet:
				return restingSince(tiles[i]).After(restingSince(tiles[j]))
			case pi == inGrid:
				oi, iok := pl.order[tiles[i].identity()]
				oj, jok := pl.order[tiles[j].identity()]
				return iok && (!jok || oi < oj)
			}
			return false
		})
	}
	return tiles
}

// railCount is how many tiles at the end of a grid-ordered slice belong in the rail.
func railCount(tiles []tile, now time.Time) int {
	n := 0
	for i := len(tiles) - 1; i >= 0 && tiles[i].place(now) != inGrid; i-- {
		n++
	}
	return n
}

// withoutTasks drops to-do tasks, for a grid too narrow for the rail: tasks are rows, never cards.
func withoutTasks(tiles []tile) []tile {
	kept := tiles[:0:0]
	for _, t := range tiles {
		if !t.isTask() {
			kept = append(kept, t)
		}
	}
	return kept
}

// needingAttention keeps the tiles that need the person, plus the tile keep (the pane being typed into,
// which must not vanish under the cursor).
func needingAttention(tiles []tile, keep string) []tile {
	var kept []tile
	for _, t := range tiles {
		if t.state().NeedsAttention() || (keep != "" && t.key == keep) {
			kept = append(kept, t)
		}
	}
	return kept
}

// livePanes counts the panes whose program is still running.
func livePanes(panes []mux.PaneInfo) int {
	n := 0
	for _, p := range panes {
		if !p.Exited {
			n++
		}
	}
	return n
}
