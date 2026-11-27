package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// Cleaning up closes the panes in one go, to start a new day on an empty grid. Closing a pane stops its
// agent, so the question says how many are in the middle of something and offers to keep those: X then
// y closes every pane, X then o only the ones at rest. Sessions in terminal tabs are left alone, as
// cockpit did not open them.

// cleanup is what closing every pane would end.
type cleanup struct {
	all     []string // the ID of every pane
	resting []string // of the panes that are neither working nor waiting for the person, and the ended ones
	working int
	waiting int
}

// cleanupOf sorts the server's panes by what closing them would interrupt. It reads every pane, not the
// tiles on screen, which a filter may have narrowed.
func (a *App) cleanupOf() cleanup {
	var c cleanup
	for _, t := range buildTiles(a.snap.Sessions, a.mc.Panes(), listView, time.Now(), placing{}) {
		if t.pane == nil {
			continue
		}
		c.all = append(c.all, t.pane.ID)
		switch {
		case !t.livePane():
			c.resting = append(c.resting, t.pane.ID)
		case t.state() == model.Busy:
			c.working++
		case t.needsYou():
			c.waiting++
		default:
			c.resting = append(c.resting, t.pane.ID)
		}
	}
	return c
}

// confirmCleanup asks before closing the panes. The question takes only its letters: ⏎ is not a yes
// here, since it ends every agent at once.
func (a *App) confirmCleanup() {
	if a.muxUnavailable() {
		return
	}
	c := a.cleanupOf()
	if len(c.all) == 0 {
		a.say("no panes to close")
		return
	}
	var busy []string
	if c.working > 0 {
		busy = append(busy, fmt.Sprintf("%d working", c.working))
	}
	if c.waiting > 0 {
		busy = append(busy, fmt.Sprintf("%d waiting for you", c.waiting))
	}
	pr := &prompt{kind: promptConfirm, strict: true, onNo: func() {}}
	pr.onYes = func() { a.closePanes(c.all) }
	switch {
	case len(busy) == 0:
		pr.label = fmt.Sprintf("close %s and stop their agents? y yes · n·esc no", allPanes(len(c.all)))
	case len(c.resting) == 0:
		pr.label = fmt.Sprintf("close %s? %s · y closes them all · n·esc no", allPanes(len(c.all)), strings.Join(busy, ", "))
	default:
		pr.label = fmt.Sprintf("close %s? %s · y closes them all · o only the %d at rest · n·esc no",
			allPanes(len(c.all)), strings.Join(busy, ", "), len(c.resting))
		pr.onO = func() { a.closePanes(c.resting) }
	}
	a.openPrompt(pr)
}

// closePanes closes the panes with these IDs and says how many.
func (a *App) closePanes(ids []string) {
	for _, id := range ids {
		if !a.send(mux.Msg{Op: mux.OpClose, ID: id}) {
			return
		}
	}
	a.mode, a.zoom = navMode, false
	a.say("closed " + panesCount(len(ids)))
}

func panesCount(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return fmt.Sprintf("%d panes", n)
}

func allPanes(n int) string {
	if n == 1 {
		return "the only pane"
	}
	return fmt.Sprintf("all %d panes", n)
}
