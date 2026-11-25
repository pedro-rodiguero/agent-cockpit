package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
