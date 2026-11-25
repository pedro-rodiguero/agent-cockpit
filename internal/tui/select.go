package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/ansi"
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

// onMouse starts, extends or finishes a selection. A click without a drag selects the card under it.
// The wheel does nothing: the panes keep no scrollback for it to move through.
func (a *App) onMouse(m mouseEvent) {
	switch {
	case m.btn >= mouseWheel:
	case m.btn == mouseLeft && !m.release:
		region, _ := a.regionAt(m.x, m.y)
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
			a.selecting = nil
			if _, i := a.regionAt(m.x, m.y); i >= 0 && a.mode == navMode {
				a.selectIndex(i)
			}
			return
		}
		a.copySelection()
	}
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
		if err := copyToClipboard(text); err != nil {
			a.post(event{kind: evFlash, msg: "copy failed: " + err.Error()})
			return
		}
		a.post(event{kind: evFlash, msg: n})
	})
}

// copyToClipboard uses pbcopy on a Mac and otherwise asks the terminal to copy, with OSC 52.
func copyToClipboard(text string) error {
	if runtime.GOOS == "darwin" && os.Getenv("SSH_TTY") == "" {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
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
