package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

func mouse(btn, x, y int, release bool) string {
	final := "M"
	if release {
		final = "m"
	}
	return fmt.Sprintf("\x1b[<%d;%d;%d%s", btn, x+1, y+1, final)
}

func TestDecodeMouse(t *testing.T) {
	var d keyDecoder
	keys := d.feed([]byte(mouse(0, 4, 2, false) + mouse(32+8, 5, 2, false) + mouse(0, 5, 2, true) + "x"))
	if len(keys) != 4 || keys[3].code != keyRune {
		t.Fatalf("keys: %+v", keys)
	}
	want := []mouseEvent{{0, 4, 2, false}, {32, 5, 2, false}, {0, 5, 2, true}}
	for i, w := range want {
		if keys[i].code != keyMouse || keys[i].mouse != w {
			t.Fatalf("key %d = %+v, want %+v", i, keys[i], w)
		}
	}
}

func TestSelectionStaysInItsCard(t *testing.T) {
	a, _ := appWithPane()
	if a.lay.cols < 2 {
		t.Fatalf("want at least two cards side by side, got %d columns", a.lay.cols)
	}
	first, second := a.lay.cell(0), a.lay.cell(1)
	y := first.y + 2
	press(a, mouse(0, first.x+3, y, false), mouse(32, second.x+5, y+1, false))
	s := a.selecting
	if s == nil || s.bx != first.x+first.w-2 || s.by != y+1 {
		t.Fatalf("selection = %+v, want it held at the first card's inner edge", s)
	}
	for _, row := range []int{y, y + 1} {
		if _, x1, ok := s.cols(row); !ok || x1 > first.x+first.w-1 {
			t.Fatalf("row %d covers up to x=%d, past the first card", row, x1)
		}
	}
	lines, _ := a.compose()
	plain := ansi.Strip(lines[y])
	a.highlight(lines)
	if got := ansi.Strip(lines[y]); got != plain || ansi.StringWidth(lines[y]) != a.w {
		t.Fatalf("highlighting changed the text:\n%q\n%q", plain, got)
	}
	if a.mode != navMode {
		t.Fatal("dragging changed the mode")
	}
}

func TestClickSelectsCard(t *testing.T) {
	a, _ := appWithPane()
	second := a.lay.cell(1)
	press(a, mouse(0, second.x+4, second.y+3, false), mouse(0, second.x+4, second.y+3, true))
	if a.cur() != a.lay.first+1 || a.selecting != nil {
		t.Fatalf("selected %d, selection %+v", a.cur(), a.selecting)
	}
}

func TestKeyClearsSelection(t *testing.T) {
	a, _ := appWithPane()
	c := a.lay.cell(0)
	press(a, mouse(0, c.x+2, c.y+2, false), mouse(32, c.x+6, c.y+2, false))
	press(a, "j")
	if a.selecting != nil {
		t.Fatal("a key left the selection on screen")
	}
}

func TestSelectedText(t *testing.T) {
	a := newTestApp(gridView, 20, 10)
	a.selecting = &selection{region: rect{x: 2, y: 0, w: 6, h: 3}, ax: 4, ay: 0, bx: 3, by: 2}
	lines := []string{"abcdefghij", paint(cInk, cBg, "klmnopqrst"), "uvwxyz    "}
	if got := a.selectedText(lines); got != "efgh\nmnopqr\nwx" {
		t.Fatalf("got %q", got)
	}
}

func TestWheelScrollsThePaneThroughItsHistory(t *testing.T) {
	a, fs := appWithPane()
	fs.screen = &mux.Screen{ID: "p1", Lines: []string{"live"}}
	c := a.lay.cell(0)
	up, down := mouse(mouseWheel, c.x+3, c.y+2, false), mouse(mouseWheel+1, c.x+3, c.y+2, false)

	press(a, up)
	if h := fs.ops(mux.OpHistory); len(h) != 1 || h[0].ID != "p1" || h[0].Rows != wheelStep {
		t.Fatalf("wheel up should ask for history: %+v", h)
	}
	fs.history = &mux.Screen{ID: "p1", Lines: []string{"older"}, Back: 3, MaxBack: 5}
	if out := ansi.Strip(strings.Join(composed(a), "\n")); !strings.Contains(out, "older") || !strings.Contains(out, "↑ 3 of 5 lines up") {
		t.Fatalf("the pane should show its history:\n%s", out)
	}
	press(a, up, up)
	if a.scrollBack["p1"] != 5 {
		t.Fatalf("scrolling stops at the first line: back %d", a.scrollBack["p1"])
	}
	press(a, down, down)
	if _, scrolled := a.scrollBack["p1"]; scrolled {
		t.Fatalf("scrolling down to the bottom should go live: back %d", a.scrollBack["p1"])
	}

	press(a, up, "\r", "x")
	if _, scrolled := a.scrollBack["p1"]; scrolled {
		t.Fatal("typing into the pane should return it to the live screen")
	}
}

func TestWheelOnAlternateScreenGoesToTheProgram(t *testing.T) {
	a, fs := appWithPane()
	fs.panes[0].Cols, fs.panes[0].Rows = 80, 24
	a.rebuild()
	fs.screen = &mux.Screen{ID: "p1", Lines: []string{"full screen app"}, AltMode: true}
	c := a.lay.cell(0)
	press(a, mouse(mouseWheel, c.x+3, c.y+2, false), mouse(mouseWheel+1, c.x+3, c.y+2, false))
	w := fs.ops(mux.OpWheel)
	if len(w) != 2 || !w[0].Up || w[1].Up || w[0].X != 2 || w[0].Y != 1 || w[0].ID != "p1" {
		t.Fatalf("wheel %+v", w)
	}
	if len(fs.ops(mux.OpInput)) != 0 || len(fs.ops(mux.OpHistory)) != 0 {
		t.Fatal("also sent keys or asked for history")
	}
}

func TestWheelOnAlternateScreenSendsArrowsToAnOldServer(t *testing.T) {
	a, fs := appWithPane()
	fs.version = 3
	fs.screen = &mux.Screen{ID: "p1", Lines: []string{"full screen app"}, AltMode: true}
	c := a.lay.cell(0)
	press(a, mouse(mouseWheel, c.x+3, c.y+2, false))
	in := fs.ops(mux.OpInput)
	if len(in) != 1 || string(in[0].Data) != "\x1b[A\x1b[A\x1b[A" || len(fs.ops(mux.OpWheel)) != 0 {
		t.Fatalf("inputs %+v", in)
	}
}

func composed(a *App) []string {
	lines, _ := a.compose()
	return lines
}

// A click on a running pane types into it; a click on another card while typing moves there.
func TestClickOnAPaneTypesIntoIt(t *testing.T) {
	a, _ := appWithPane()
	pane, sess := a.lay.cell(0), a.lay.cell(1)
	press(a, mouse(0, pane.x+4, pane.y+3, false), mouse(0, pane.x+4, pane.y+3, true))
	if a.mode != insertMode || a.sel != paneKey("p1") {
		t.Fatalf("mode %v selected %s", a.mode, a.sel)
	}
	press(a, mouse(0, sess.x+4, sess.y+3, false), mouse(0, sess.x+4, sess.y+3, true))
	if a.mode != navMode || a.cur() != 1 {
		t.Fatalf("a session card took the keyboard: mode %v selected %d", a.mode, a.cur())
	}
}

// Two clicks copy the word or URL under the pointer, without what wraps it; three copy the line.
func TestDoubleAndTripleClickCopy(t *testing.T) {
	var mu sync.Mutex
	var copied []string
	clipboard = func(s string) error { mu.Lock(); copied = append(copied, s); mu.Unlock(); return nil }
	got := func(n int) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(copied) < n {
			return "", false
		}
		return copied[n-1], true
	}
	t.Cleanup(func() { clipboard = copyToClipboard })
	a, fs := appWithPane()
	fs.screen = &mux.Screen{ID: "p1", Lines: []string{"see (https://example.com/a?b=1). ok"}}
	a.relayout()
	c := a.lay.cell(0)
	lines, _ := a.compose()
	y, x := -1, -1
	for row, l := range lines {
		if i := strings.Index(ansi.Strip(l), "example"); i >= 0 && row >= c.y && row < c.y+c.h {
			y, x = row, ansi.StringWidth(ansi.Strip(l)[:i])
			break
		}
	}
	if y < 0 {
		t.Fatal("the pane line is not on screen")
	}
	click := func() { press(a, mouse(0, x, y, false), mouse(0, x, y, true)) }
	click()
	click()
	wait := func(n int) string {
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if s, ok := got(n); ok {
				return s
			}
		}
		t.Fatalf("copy %d never happened", n)
		return ""
	}
	if got := wait(1); got != "https://example.com/a?b=1" {
		t.Fatalf("double click copied %q", got)
	}
	click()
	if got := wait(2); got != "see (https://example.com/a?b=1). ok" {
		t.Fatalf("triple click copied %q", got)
	}
}

// A click on the rail while typing into a pane leaves the pane, as ctrl-q does.
func TestClickOnTheRailLeavesThePane(t *testing.T) {
	a, _ := appWithPane()
	a.snap.Sessions[1].State = model.Idle
	a.snap.Sessions[1].Since = time.Now().Add(-2 * quietAfter) // goes to the rail
	a.rebuild()
	a.relayout()
	if a.lay.rail.w == 0 {
		t.Fatal("no rail to click")
	}
	a.sel = paneKey("p1")
	press(a, "\r")
	if a.mode != insertMode {
		t.Fatal("not typing into the pane")
	}
	r := a.lay.rail
	press(a, mouse(0, r.x+5, r.y+r.h-2, false), mouse(0, r.x+5, r.y+r.h-2, true))
	if a.mode != navMode || a.zoom {
		t.Fatalf("still typing: mode %v zoom %v", a.mode, a.zoom)
	}
}
