package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/quick"
)

// Mouse buttons as SGR mouse reporting encodes them.
const (
	mouseLeft   = 0
	mouseMotion = 32 // added to the button while it is held and the pointer moves
	mouseWheel  = 64 // wheel up; 65 is wheel down
)

// mouseEvent is one SGR mouse report, in 0-based screen cells.
type mouseEvent struct {
	btn     int
	x, y    int
	release bool
}

// selection is text being selected with the mouse. It never leaves the region it started in (the
// inside of a card, the rail, the list's reading pane), so dragging over a pane never picks up the
// pane beside it, as the terminal's own selection would.
type selection struct {
	region   rect
	ax, ay   int // where the drag started
	bx, by   int // where it is now
	dragging bool
}

// span returns the selection's start and end in reading order.
func (s *selection) span() (sx, sy, ex, ey int) {
	if s.ay < s.by || (s.ay == s.by && s.ax <= s.bx) {
		return s.ax, s.ay, s.bx, s.by
	}
	return s.bx, s.by, s.ax, s.ay
}

// cols is the cell range [x0, x1) the selection covers on screen row y, if any.
func (s *selection) cols(y int) (x0, x1 int, ok bool) {
	sx, sy, ex, ey := s.span()
	if y < sy || y > ey {
		return 0, 0, false
	}
	x0, x1 = s.region.x, s.region.x+s.region.w
	if y == sy {
		x0 = sx
	}
	if y == ey {
		x1 = ex + 1
	}
	return x0, x1, x1 > x0
}

func (s *selection) empty() bool { return s.ax == s.bx && s.ay == s.by }

func (r rect) contains(x, y int) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

func (r rect) clamp(x, y int) (int, int) {
	return max(r.x, min(r.x+r.w-1, x)), max(r.y, min(r.y+r.h-1, y))
}

// regionAt is the part of the screen a selection starting at (x, y) stays in, and the tile shown
// there, -1 for none.
func (a *App) regionAt(x, y int) (rect, int) {
	l := a.lay
	body := rect{x: 0, y: l.bodyTop, w: a.w, h: l.bodyH}
	switch {
	case !body.contains(x, y):
		return rect{x: 0, y: y, w: a.w, h: 1}, -1
	case a.help:
		return body, -1
	case l.rail.w > 0 && l.rail.contains(x, y):
		return rect{x: l.rail.x + 1, y: l.rail.y, w: l.rail.w - 1, h: l.rail.h}, -1
	case a.view == listView:
		paneTop := l.bodyTop + listHeadRows + l.perPage + 1
		if y >= paneTop {
			return rect{x: 0, y: paneTop, w: a.w, h: l.bodyTop + l.bodyH - paneTop}, -1
		}
		if i := l.first + y - l.bodyTop - listHeadRows; y >= l.bodyTop+listHeadRows && i < l.first+l.count {
			return rect{x: 0, y: y, w: a.w, h: 1}, i
		}
		return rect{x: 0, y: l.bodyTop, w: a.w, h: paneTop - l.bodyTop}, -1
	}
	for k := 0; k < l.count; k++ {
		if c := l.cell(k); c.contains(x, y) {
			inner := rect{x: c.x + 1, y: c.y + 1, w: max(1, c.w-2), h: max(1, c.h-2)}
			return inner, l.first + k
		}
	}
	return body, -1
}

// onMouse starts, extends or finishes a selection. A click without a drag selects the card under it,
// and the wheel scrolls the pane under the pointer.
func (a *App) onMouse(m mouseEvent) {
	switch {
	case m.btn >= mouseWheel:
		a.wheel(m)
	case a.doc != nil:
		// The viewer covers the cards: a click must not select one the person cannot see.
	case m.btn == mouseLeft && !m.release && a.startDrag(m.x, m.y):
	case m.btn == mouseLeft+mouseMotion && a.drag != nil:
		a.dragTo(m.x, m.y)
	case m.btn == mouseLeft && m.release && a.drag != nil:
		a.endDrag(m.x, m.y)
	case m.btn == mouseLeft && !m.release:
		region, _ := a.regionAt(m.x, m.y)
		a.countClick(m.x, m.y, time.Now())
		a.selecting = &selection{region: region, ax: m.x, ay: m.y, bx: m.x, by: m.y, dragging: true}
	case m.btn == mouseLeft+mouseMotion:
		if s := a.selecting; s != nil && s.dragging {
			s.bx, s.by = s.region.clamp(m.x, m.y)
		}
	case m.btn == mouseLeft && m.release:
		s := a.selecting
		if s == nil || !s.dragging {
			return
		}
		s.dragging = false
		if s.empty() {
			switch a.click.n {
			case 2:
				a.selectWord(s.region, m.x, m.y)
			case 3:
				a.selectLine(s.region, m.y)
			default:
				a.selecting = nil
				a.clickCard(m.x, m.y)
			}
			return
		}
		a.copySelection()
	}
}

// multiClick is how soon a click must follow the previous one, on the same cell, to count with it.
const multiClick = 400 * time.Millisecond

// clicks counts consecutive clicks on one cell: two select a word, three a line.
type clicks struct {
	x, y, n int
	at      time.Time
}

func (a *App) countClick(x, y int, now time.Time) {
	c := &a.click
	if c.n > 0 && c.n < 3 && x == c.x && y == c.y && now.Sub(c.at) < multiClick {
		c.n++
	} else {
		c.n = 1
	}
	c.x, c.y, c.at = x, y, now
}

// clickCard selects the card under (x, y). A running pane takes the keyboard, as clicking into a
// terminal would; any other card is only selected, since what ⏎ does there (start, resume, reopen)
// should not happen on a stray click.
func (a *App) clickCard(x, y int) {
	_, i := a.regionAt(x, y)
	if i < 0 || a.mode == promptMode {
		return
	}
	a.flushTyped()
	a.selectIndex(i)
	if t := a.tiles[i]; t.livePane() && a.mc != nil && a.view == gridView && !a.lay.inRail(i) {
		a.wake(t.pane.ID)
		a.mode = insertMode
		return
	}
	a.mode = navMode
}

// selectWord selects and copies the run of non-blank cells under (x, y) in region, without the
// brackets, quotes and punctuation around it: a double click on a URL copies the URL.
func (a *App) selectWord(region rect, x, y int) {
	lines, _ := a.compose()
	if y < 0 || y >= len(lines) {
		a.selecting = nil
		return
	}
	cell := func(cx int) string { return ansi.Strip(ansi.Cut(lines[y], cx, cx+1)) }
	blank := func(cx int) bool { c := cell(cx); return c == "" || c == " " }
	if blank(x) {
		a.selecting = nil
		return
	}
	x0, x1 := x, x
	for x0 > region.x && !blank(x0-1) {
		x0--
	}
	for x1 < region.x+region.w-1 && !blank(x1+1) {
		x1++
	}
	for x0 < x1 && strings.Contains(wordEdge, cell(x0)) {
		x0++
	}
	for x1 > x0 && strings.Contains(wordEdge+".,;:!?", cell(x1)) {
		x1--
	}
	a.selecting = &selection{region: region, ax: x0, ay: y, bx: x1, by: y}
	a.copySelection()
}

// wordEdge are characters that wrap a word or a URL in text rather than belong to it.
const wordEdge = "()[]{}<>\"'`"

// selectLine selects and copies row y of region.
func (a *App) selectLine(region rect, y int) {
	a.selecting = &selection{region: region, ax: region.x, ay: y, bx: region.x + region.w - 1, by: y}
	a.copySelection()
}

// wheelStep is how many lines one notch of the wheel scrolls.
const wheelStep = 3

// mouseMods are the bits SGR mouse reporting adds to the button for shift, alt and ctrl.
const mouseMods = 4 | 8 | 16

// wheel scrolls the pane under the pointer up into its history and back down to the live screen. A
// program on the alternate screen has no history there, so it gets arrow keys instead, as terminals
// do.
func (a *App) wheel(m mouseEvent) {
	up := m.btn&^mouseMods == mouseWheel
	if a.doc != nil {
		if up {
			a.doc.scroll = max(0, a.doc.scroll-wheelStep)
		} else {
			a.doc.scroll = min(max(0, len(a.doc.lines)-1), a.doc.scroll+wheelStep)
		}
		return
	}
	t, ok := a.tileAt(m.x, m.y)
	if !ok || t.pane == nil || a.mc == nil {
		return
	}
	id := t.pane.ID
	back := a.scrollBack[id]
	if scr := a.mc.Screen(id); scr != nil && scr.AltMode && back == 0 {
		if !t.livePane() {
			return
		}
		if a.serverOlderThan(4) {
			arrow := "\x1b[B"
			if up {
				arrow = "\x1b[A"
			}
			a.send(mux.Msg{Op: mux.OpInput, ID: id, Data: []byte(strings.Repeat(arrow, wheelStep))})
			return
		}
		r, _ := a.regionAt(m.x, m.y)
		x := min(max(0, m.x-r.x), max(0, t.pane.Cols-1))
		y := min(max(0, m.y-r.y), max(0, t.pane.Rows-1))
		a.send(mux.Msg{Op: mux.OpWheel, ID: id, Up: up, X: x, Y: y})
		return
	}
	if a.serverOlderThan(3) {
		a.say("scrolling back needs the newer pane server · it restarts on its own when its last pane closes")
		return
	}
	if up {
		back += wheelStep
	} else {
		back -= wheelStep
	}
	if h := a.mc.History(id); h != nil {
		back = min(back, h.MaxBack)
	}
	if back <= 0 {
		delete(a.scrollBack, id)
		return
	}
	if a.scrollBack == nil {
		a.scrollBack = map[string]int{}
	}
	a.scrollBack[id] = back
	a.send(mux.Msg{Op: mux.OpHistory, ID: id, Rows: back})
}

// tileAt is the tile drawn at (x, y): a card in the grid, or the selected tile, whose pane fills the
// zoom and the list's reading pane.
func (a *App) tileAt(x, y int) (tile, bool) {
	if _, i := a.regionAt(x, y); i >= 0 && i < len(a.tiles) && a.view == gridView && !a.zoom {
		return a.tiles[i], true
	}
	if a.lay.bodyTop <= y && y < a.lay.bodyTop+a.lay.bodyH && (a.view == listView || a.zoom) {
		return a.selected()
	}
	return tile{}, false
}

// serverOlderThan reports whether the pane server is known to speak a protocol older than version.
func (a *App) serverOlderThan(version int) bool {
	c, ok := a.mc.(interface{ ServerVersion() (int, bool) })
	if !ok {
		return false
	}
	v, known := c.ServerVersion()
	return known && v < version
}

// selectedText is the text under the selection, each row with its trailing blanks removed.
func (a *App) selectedText(lines []string) string {
	s := a.selecting
	_, sy, _, ey := s.span()
	var rows []string
	for y := sy; y <= ey && y < len(lines); y++ {
		x0, x1, ok := s.cols(y)
		if !ok {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, strings.TrimRight(ansi.Strip(ansi.Cut(lines[y], x0, x1)), " "))
	}
	return strings.Join(rows, "\n")
}

func (a *App) copySelection() {
	lines, _ := a.compose()
	text := a.selectedText(lines)
	if strings.TrimSpace(text) == "" {
		a.selecting = nil
		return
	}
	n := fmt.Sprintf("copied %d characters", len([]rune(text)))
	a.goSafe(func() {
		if err := clipboard(text); err != nil {
			a.post(event{kind: evFlash, msg: "copy failed: " + err.Error()})
			return
		}
		a.post(event{kind: evFlash, msg: n})
	})
}

// clipboard copies text; tests swap it so they never touch the person's clipboard.
var clipboard = copyToClipboard

// copyToClipboard uses pbcopy on a Mac and otherwise asks the terminal to copy, with OSC 52.
func copyToClipboard(text string) error {
	if runtime.GOOS == "darwin" && os.Getenv("SSH_TTY") == "" {
		return quick.Run(text, "pbcopy")
	}
	_, err := os.Stdout.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a")
	return err
}

// highlight paints the selection over the composed frame.
func (a *App) highlight(lines []string) {
	s := a.selecting
	if s == nil || s.empty() {
		return
	}
	for y := range lines {
		x0, x1, ok := s.cols(y)
		if !ok {
			continue
		}
		l := lines[y]
		mid := ansi.Strip(ansi.Cut(l, x0, x1))
		lines[y] = ansi.Cut(l, 0, x0) + paint(cBg, cGold, mid) + ansi.Cut(l, x1, a.w)
	}
}
