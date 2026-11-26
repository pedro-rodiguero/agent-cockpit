package tui

import (
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

func gridKeys(a *App) []string {
	var out []string
	for _, t := range a.tiles[:a.lay.mainN] {
		out = append(out, t.key)
	}
	return out
}

func TestMoveCardsWithKeys(t *testing.T) {
	a, _ := paneGrid(t, 3)
	if a.lay.cols != 3 || a.lay.rows != 1 {
		t.Fatalf("grid %d×%d", a.lay.cols, a.lay.rows)
	}
	press(a, "L")
	if got := gridKeys(a); !equal(got, []string{"pane:p2", "pane:p1", "pane:p3"}) {
		t.Fatalf("after L: %v", got)
	}
	press(a, "\x1b[1;2C") // shift-right
	if got := gridKeys(a); !equal(got, []string{"pane:p2", "pane:p3", "pane:p1"}) || a.sel != paneKey("p1") {
		t.Fatalf("after shift-right: %v, selected %s", got, a.sel)
	}
	press(a, "L") // already last: nothing to swap with
	press(a, "H", "H")
	if got := gridKeys(a); !equal(got, []string{"pane:p1", "pane:p2", "pane:p3"}) {
		t.Fatalf("after H H: %v", got)
	}
	press(a, "L")

	// The order survives a restart.
	b, bfs := appWithPane()
	b.arr = loadArrangement(a.arr.path)
	bfs.panes = a.panes
	b.snap.Sessions = nil
	b.rebuild()
	b.relayout()
	if got := gridKeys(b); !equal(got, []string{"pane:p2", "pane:p1", "pane:p3"}) {
		t.Fatalf("after a restart: %v", got)
	}
}

func TestGrowAndEvenSizes(t *testing.T) {
	a, fs := paneGrid(t, 3)
	even := a.lay.cell(0)
	press(a, ">")
	big, other := a.lay.cell(0), a.lay.cell(1)
	if big.w <= even.w || big.w <= other.w || big.w+other.w+a.lay.cell(2).w != a.lay.gridW {
		t.Fatalf("grown card %+v, neighbour %+v", big, other)
	}
	var resized bool
	for _, m := range fs.ops(mux.OpResize) {
		if m.ID == "p1" && m.Cols == big.w-2 {
			resized = true
		}
	}
	if !resized {
		t.Fatalf("the pane was not resized to its card: %+v", fs.ops(mux.OpResize))
	}
	lines, _ := a.compose()
	for y, ln := range lines {
		if w := width(ln); w != a.w {
			t.Fatalf("row %d is %d cells wide, not %d", y, w, a.w)
		}
	}
	for range 20 {
		press(a, ">")
	}
	if w := a.lay.cell(1).w; w < minCellW {
		t.Fatalf("a neighbour shrank below the smallest card: %d", w)
	}
	press(a, "=")
	if a.lay.cells != nil || a.lay.cell(0) != even {
		t.Fatalf("= left %+v", a.lay.cell(0))
	}
}

func TestDragTheBorderBetweenCards(t *testing.T) {
	a, _ := paneGrid(t, 3)
	c := a.lay.cell(0)
	x, y := c.x+c.w-1, c.y+3
	press(a, mouse(mouseLeft, x, y, false), mouse(mouseLeft+mouseMotion, x+10, y, false), mouse(mouseLeft, x+10, y, true))
	if got := a.lay.cell(0).w; got < c.w+9 || got > c.w+11 {
		t.Fatalf("width %d after dragging %d by 10", got, c.w)
	}
	if a.selecting != nil {
		t.Fatal("the drag selected text")
	}

	b, _ := paneGrid(t, 4) // 2×2
	top := b.lay.cell(0)
	y = top.y + top.h - 1
	press(b, mouse(mouseLeft, top.x+5, y, false), mouse(mouseLeft+mouseMotion, top.x+5, y+5, false), mouse(mouseLeft, top.x+5, y+5, true))
	if got := b.lay.cell(0).h; got < top.h+4 || got > top.h+6 || b.lay.cell(1).h != got {
		t.Fatalf("row height %d after dragging %d by 5", got, top.h)
	}
}

func TestDragACardByItsTitle(t *testing.T) {
	a, _ := paneGrid(t, 3)
	from, to := a.lay.cell(0), a.lay.cell(2)
	press(a, mouse(mouseLeft, from.x+5, from.y, false), mouse(mouseLeft+mouseMotion, to.x+5, to.y+3, false), mouse(mouseLeft, to.x+5, to.y+3, true))
	if got := gridKeys(a); !equal(got, []string{"pane:p3", "pane:p2", "pane:p1"}) || a.sel != paneKey("p1") {
		t.Fatalf("after the drop: %v, selected %s", got, a.sel)
	}

	// A click on a title selects the card, as before.
	c := a.lay.cell(1)
	press(a, mouse(mouseLeft, c.x+5, c.y, false), mouse(mouseLeft, c.x+5, c.y, true))
	if a.sel != paneKey("p2") {
		t.Fatalf("selected %s", a.sel)
	}
}

func TestSplitShares(t *testing.T) {
	for _, tc := range []struct {
		total   int
		weights []float64
		least   int
		want    []int
	}{
		{90, []float64{1, 1, 1}, 10, []int{30, 30, 30}},
		{100, []float64{1, 9}, 40, []int{40, 60}},
		{100, []float64{3, 1}, 10, []int{75, 25}},
		{50, []float64{1, 1, 1}, 40, nil}, // no room for the minimum: share what there is
	} {
		got := splitShares(tc.total, tc.weights, tc.least)
		sum := 0
		for _, v := range got {
			sum += v
		}
		if sum != tc.total || (tc.want != nil && !equalInts(got, tc.want)) {
			t.Errorf("splitShares(%d, %v, %d) = %v", tc.total, tc.weights, tc.least, got)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
