package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// Parking moves a shell pane that only holds an SSH connection out of the grid and into the rail, so a
// tunnel or a quiet login does not take the room of a pane being worked in. cockpit asks first: once a
// tunnel (ssh -N) has been quiet for a minute, once any other SSH session has been quiet for ten.
// "Always" answers are remembered, for every tunnel or for one host; ⏎ on a parked pane brings it back.
const (
	tunnelQuietAfter = time.Minute
	sshQuietAfter    = 10 * time.Minute
)

// sshCommand describes a foreground command line when it is an SSH connection.
type sshCommand struct {
	host   string
	tunnel bool // ssh -N: forwards only, nothing to type into
}

// sshOptionsWithValue are the ssh(1) options that take a value, as in "-p 22" or "-p22".
const sshOptionsWithValue = "BbcDEeFIiJLlMmOoPpQRSWw"

// parseSSH reads an ssh, autossh or mosh command line. ok is false for anything else.
func parseSSH(cmdline string) (c sshCommand, ok bool) {
	f := strings.Fields(cmdline)
	if len(f) == 0 {
		return c, false
	}
	switch filepath.Base(f[0]) {
	case "ssh", "autossh", "mosh":
	default:
		return c, false
	}
	for i := 1; i < len(f); i++ {
		a := f[i]
		if a == "--" {
			if i+1 < len(f) {
				c.host = f[i+1]
			}
			break
		}
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			c.host = a
			break
		}
		if strings.HasPrefix(a, "--") { // mosh's long options; --ssh=… carries its value
			continue
		}
		for j := 1; j < len(a); j++ {
			if a[j] == 'N' {
				c.tunnel = true
			}
			if strings.IndexByte(sshOptionsWithValue, a[j]) >= 0 {
				if j == len(a)-1 {
					i++ // the value is the next argument
				}
				break
			}
		}
	}
	if at := strings.LastIndexByte(c.host, '@'); at >= 0 {
		c.host = c.host[at+1:]
	}
	return c, c.host != ""
}

// parking is what cockpit remembers about parking, in parking.json next to the to-do list.
type parking struct {
	path string

	AlwaysTunnels bool            `json:"alwaysTunnels,omitempty"` // park every quiet tunnel without asking
	AlwaysHosts   map[string]bool `json:"alwaysHosts,omitempty"`   // park quiet SSH sessions to these hosts without asking
	Parked        map[string]bool `json:"parked,omitempty"`        // SSH panes in the rail, by paneIdentity
	Declined      map[string]bool `json:"declined,omitempty"`      // panes the person keeps in the grid
	Aside         map[string]bool `json:"aside,omitempty"`         // any pane the person set aside with s
}

// inRail is every pane parking keeps in the rail, by paneIdentity.
func (p *parking) inRail() map[string]bool {
	if len(p.Aside) == 0 {
		return p.Parked
	}
	all := make(map[string]bool, len(p.Parked)+len(p.Aside))
	for k := range p.Parked {
		all[k] = true
	}
	for k := range p.Aside {
		all[k] = true
	}
	return all
}

func loadParking(path string) *parking {
	p := &parking{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, p)
	}
	if p.AlwaysHosts == nil {
		p.AlwaysHosts = map[string]bool{}
	}
	if p.Parked == nil {
		p.Parked = map[string]bool{}
	}
	if p.Declined == nil {
		p.Declined = map[string]bool{}
	}
	if p.Aside == nil {
		p.Aside = map[string]bool{}
	}
	return p
}

// save writes the file whole, through a temporary file, so a crash leaves the old one.
func (p *parking) save(panes []mux.PaneInfo) {
	live := map[string]bool{}
	for _, pn := range panes {
		live[paneIdentity(pn)] = true
	}
	for k := range p.Parked {
		if !live[k] {
			delete(p.Parked, k)
		}
	}
	for _, m := range []map[string]bool{p.Declined, p.Aside} {
		for k := range m {
			if !live[k] {
				delete(m, k)
			}
		}
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(p.path), 0o700) != nil {
		return
	}
	tmp := p.path + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
		_ = os.Rename(tmp, p.path)
	}
}

// paneIdentity names a pane across cockpit restarts; the start time tells apart the panes of a pane
// server started later, which numbers its panes from 1 again.
func paneIdentity(p mux.PaneInfo) string {
	return p.ID + "@" + strconv.FormatInt(p.Started.UnixNano(), 10)
}

// quietSince is when a pane last took input or printed anything.
func quietSince(p mux.PaneInfo) time.Time {
	if p.LastIO.After(p.Started) {
		return p.LastIO
	}
	return p.Started
}

// tendParking runs after every collection: it brings back parked panes whose SSH connection ended,
// parks quiet ones the person said to always park, and asks about one other, when nothing else is
// going on.
func (a *App) tendParking(now time.Time) {
	if a.park == nil {
		return
	}
	changed := false
	var ask *mux.PaneInfo
	var askSSH sshCommand
	for i := range a.panes {
		pn := &a.panes[i]
		id := paneIdentity(*pn)
		c, isSSH := parseSSH(pn.Foreground)
		if a.park.Parked[id] {
			if pn.Exited || !isSSH {
				delete(a.park.Parked, id) // the connection ended: the pane is a shell to use again
				changed = true
			}
			continue
		}
		if pn.Exited || !isSSH || pn.Agent != "shell" || a.park.Declined[id] {
			continue
		}
		wait := sshQuietAfter
		if c.tunnel {
			wait = tunnelQuietAfter
		}
		if now.Sub(quietSince(*pn)) < wait || now.Before(a.parkSnooze[id]) {
			continue
		}
		if (c.tunnel && a.park.AlwaysTunnels) || a.park.AlwaysHosts[c.host] {
			a.park.Parked[id] = true
			changed = true
			a.say("moved " + sshLabel(c) + " to the rail · ⏎ on it brings it back")
			continue
		}
		if ask == nil {
			ask, askSSH = pn, c
		}
	}
	if changed {
		a.park.save(a.panes)
		a.rebuild()
	}
	if ask != nil && a.mode == navMode && a.prompt == nil && !a.help && a.doc == nil && a.selecting == nil {
		a.askToPark(*ask, askSSH, now)
	}
}

func sshLabel(c sshCommand) string {
	if c.tunnel {
		return "the ssh tunnel to " + c.host
	}
	return "ssh to " + c.host
}

// askToPark asks whether to move a quiet SSH pane to the rail. Any key but an answer leaves the
// question for later and does what it would have done.
func (a *App) askToPark(pn mux.PaneInfo, c sshCommand, now time.Time) {
	id := paneIdentity(pn)
	always := "a always for " + c.host
	if c.tunnel {
		always = "a always move tunnels"
	}
	label := sshLabel(c) + " has been quiet " + ago(quietSince(pn), now) + " · move it to the rail? y yes · n no · " + always
	a.openPrompt(&prompt{kind: promptConfirm, label: label, strict: true,
		onYes: func() { a.parkPane(id, "") },
		onNo: func() {
			a.park.Declined[id] = true
			a.park.save(a.panes)
			a.say("kept in the grid; cockpit will not ask about this pane again")
		},
		onAll: func() {
			if c.tunnel {
				a.park.AlwaysTunnels = true
				a.parkPane(id, "quiet ssh tunnels will move to the rail from now on")
			} else {
				a.park.AlwaysHosts[c.host] = true
				a.parkPane(id, "quiet ssh sessions to "+c.host+" will move to the rail from now on")
			}
		},
		onOther: func() { a.parkSnooze[id] = time.Now().Add(sshQuietAfter) },
	})
}

func (a *App) parkPane(id, msg string) {
	a.park.Parked[id] = true
	a.park.save(a.panes)
	a.rebuild()
	if msg == "" {
		msg = "moved to the rail · ⏎ on it brings it back"
	}
	a.say(msg)
}

// unpark brings a pane in the rail back to the grid, as ⏎ on it does: a parked or set-aside pane, or
// one that went there by being quiet, which counts as quiet again from now.
func (a *App) unpark(t tile) bool {
	if t.pane == nil {
		return false
	}
	back := t.place(time.Now()) == inQuiet
	a.wake(t.pane.ID)
	if a.park != nil && t.parked {
		id := paneIdentity(*t.pane)
		delete(a.park.Parked, id)
		delete(a.park.Aside, id)
		a.park.save(a.panes)
		back = true
	}
	a.rebuild()
	return back
}

// wake restarts a pane's quiet: it stays in the grid at least quietAfter from now.
func (a *App) wake(id string) {
	if a.woke == nil {
		a.woke = map[string]time.Time{}
	}
	a.woke[id] = time.Now()
}

// toggleAside sets the selected pane aside in the rail, or brings a pane in the rail back to the grid.
func (a *App) toggleAside() {
	t, ok := a.selected()
	switch {
	case !ok || !t.livePane():
		a.say("only a running pane can be set aside")
	case a.view != gridView:
		a.say("panes are set aside in the grid view · v switches to it")
	case t.place(time.Now()) != inGrid:
		a.unpark(t)
		a.say("back in the grid")
	case a.park == nil:
	default:
		a.park.Aside[paneIdentity(*t.pane)] = true
		a.park.save(a.panes)
		a.rebuild()
		a.say("set aside in the rail · s or ⏎ on it brings it back")
	}
}
