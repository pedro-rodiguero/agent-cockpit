package tui

import (
	"path/filepath"
	"sort"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// tile is one card: a collected session, a pane, or both when the pane runs the session's process.
type tile struct {
	key  string // stable identity across rebuilds: the session ID, or paneKey(pane ID)
	sess *model.Session
	pane *mux.PaneInfo
}

func paneKey(id string) string { return "pane:" + id }

func (t tile) state() model.State {
	if t.sess != nil {
		return t.sess.State
	}
	return model.Idle
}

// isItem reports whether the tile is an inbox item from an external source rather than an agent.
func (t tile) isItem() bool { return t.pane == nil && t.sess != nil && t.sess.State == model.Inbox }

// livePane reports whether the tile is a pane whose program is still running.
func (t tile) livePane() bool { return t.pane != nil && !t.pane.Exited }

// canReply reports whether a message can be sent to the session over Claude Code's peer socket.
func (t tile) canReply() bool {
	return t.sess != nil && t.sess.Agent == "claude" && t.sess.Socket != ""
}

// canAdopt reports whether the session runs in a terminal tab and can be resumed in a pane.
func (t tile) canAdopt() bool { return t.pane == nil && t.sess != nil && resumable(*t.sess) == nil }

func (t tile) name() string {
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

// buildTiles merges collected sessions with the server's panes. A pane whose process is a session's
// process (the agent itself, since the pane's shell execs it) becomes one tile with both. Panes come
// first; in the grid the remaining sessions are ordered by start time so cards keep their places, with
// inbox items last. The list keeps the collector's order, most urgent first.
func buildTiles(sessions []model.Session, panes []mux.PaneInfo, view viewKind) []tile {
	byPID := map[int]int{}
	for i, s := range sessions {
		if s.PID != 0 {
			byPID[s.PID] = i
		}
	}
	merged := map[int]bool{}
	tiles := make([]tile, 0, len(sessions)+len(panes))
	for i := range panes {
		t := tile{key: paneKey(panes[i].ID), pane: &panes[i]}
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
		sort.SliceStable(rest, func(i, j int) bool {
			if ii, ij := rest[i].isItem(), rest[j].isItem(); ii != ij {
				return ij
			}
			return rest[i].sess.Started.Before(rest[j].sess.Started)
		})
	}
	return append(tiles, rest...)
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
