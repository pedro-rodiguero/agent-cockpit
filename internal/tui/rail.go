package tui

import (
	"fmt"
	"strings"
	"time"
)

// The rail is a column on the right of the grid for what is not happening now. It is drawn on the
// panel background, one section per place, each tile a two-row entry:
//
//	│ WORK                              2
//	│ □ Ship the billing export     today
//	│   BILL-12 · in progress
//	│
//	│ INBOX                             1
//	│ ✉ payments                       3h
//	│   review requested · Retry webhooks…
//	│
//	│ IDLE 1h+                          5
//	│ ○ Migrate to Postgres 17        19h
//	│   ~/src/db · s002
const (
	railEdge      = "│"
	railEntryRows = 2
)

var railSections = map[place]string{inTodo: "TO DO", inInbox: "INBOX", inEnded: "ENDED", inParked: "PARKED"}

// railSection is the section a tile is listed under: its place, and for tasks the group they belong to.
func railSection(t tile, now time.Time) string {
	p := t.place(now)
	if p == inTodo && t.sess.Group != "" {
		return strings.ToUpper(t.sess.Group)
	}
	if p == inQuiet {
		return "IDLE " + shortDuration(quietAfter) + "+"
	}
	return railSections[p]
}

// railBody draws the rail's tiles, tiles[mainN:], into r. When they do not fit, the rows scroll so
// the selected entry stays in view.
func (a *App) railBody(r rect, now time.Time) []string {
	type span struct{ from, to int }
	var rows []string
	add := func(s string) { rows = append(rows, s) }
	line := func(s string, bg rgb) string {
		return paint(cLine, cBg, railEdge) + pad(s, r.w-1, bg)
	}
	sel, selSpan := a.cur(), span{}
	tiles := a.tiles[a.lay.mainN:]
	for i, t := range tiles {
		section := railSection(t, now)
		if i == 0 || railSection(tiles[i-1], now) != section {
			if i > 0 {
				add(line("", cPanel))
			}
			n := 0
			for _, u := range tiles[i:] {
				if railSection(u, now) == section {
					n++
				}
			}
			label, count := " "+section, fmt.Sprintf("%d ", n)
			add(line(paint(cFaint, cPanel, label+strings.Repeat(" ", max(1, r.w-1-width(label)-width(count)))+count), cPanel))
		}
		idx := a.lay.mainN + i
		if idx == sel {
			selSpan = span{len(rows), len(rows) + railEntryRows}
		}
		for _, row := range railEntry(t, r.w-1, idx == sel, now) {
			add(paint(cLine, cBg, railEdge) + row)
		}
	}
	top := 0
	if selSpan.to > r.h {
		top = selSpan.to - r.h
	}
	out := rows[min(top, len(rows)):]
	if len(out) > r.h {
		out = out[:r.h]
	}
	for len(out) < r.h {
		out = append(out, line("", cPanel))
	}
	return out
}

// railEntry is a tile's two rows in the rail, w cells wide: state, name and age; then where it runs,
// or what an item is about.
func railEntry(t tile, w int, selected bool, now time.Time) []string {
	bg, mark, nameFg := cPanel, " ", cSoft
	st := t.state()
	l := look(st)
	ageFg := cFaint
	if st.NeedsAttention() {
		nameFg, ageFg = cInk, l.color
	}
	if selected {
		bg, mark, nameFg = cPanelHi, "▌", cGoldHi
	}
	glyph, glyphFg := l.glyph, l.color
	age := ago(since(t), now)
	var detail string
	switch {
	case t.pane != nil && t.pane.Exited:
		glyph, glyphFg = "◧", cFaint
		age = fmt.Sprintf("code %d", t.pane.ExitCode)
		detail = t.pane.Agent + " · " + tilde(t.pane.Cwd)
	case t.pane != nil:
		if _, ssh := parseSSH(t.pane.Foreground); ssh {
			glyph, glyphFg = "⇄", cIdle
		}
		age = ago(t.rested, now)
		detail = firstOf(t.pane.Foreground, t.pane.Agent+" · "+tilde(t.pane.Cwd))
	case t.isTask():
		var dueFg rgb
		age, dueFg = due(t.sess.Due, now)
		ageFg = dueFg
		detail = t.sess.Detail
	case t.isItem():
		detail = firstOf(t.sess.Detail, "item")
		if t.sess.Title != "" {
			detail += " · " + t.sess.Title
		}
	default:
		parts := []string{tilde(t.sess.Cwd)}
		if tty := strings.TrimPrefix(t.sess.TTY, "tty"); tty != "" {
			parts = append(parts, tty)
		}
		detail = strings.Join(parts, " · ")
	}
	right := " " + age + " "
	nameW := max(1, w-3-width(right))
	first := paint(cGold, bg, mark) + paint(glyphFg, bg, glyph+" ") + paint(nameFg, bg, fit(t.name(), nameW)) + paint(ageFg, bg, right)
	second := paint(cFaint, bg, "   "+fit(detail, max(1, w-4)))
	return []string{pad(first, w, bg), pad(second, w, bg)}
}

// due says when a task is due relative to today ("today", "tomorrow", "in 3d", "2d late"), in a color
// that rises as the date comes closer. A task without a due date says nothing.
func due(at, now time.Time) (string, rgb) {
	if at.IsZero() {
		return "", cFaint
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	ay, am, ad := at.In(now.Location()).Date()
	days := int(time.Date(ay, am, ad, 0, 0, 0, 0, now.Location()).Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return fmt.Sprintf("%dd late", -days), cStuck
	case days == 0:
		return "today", cWaiting
	case days == 1:
		return "tomorrow", cSoft
	default:
		return fmt.Sprintf("in %dd", days), cFaint
	}
}
