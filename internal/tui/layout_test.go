package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestBestGridRespectsCaps(t *testing.T) {
	sizes := [][2]int{{80, 20}, {190, 47}, {400, 20}, {40, 200}, {120, 37}}
	for _, sz := range sizes {
		W, H := sz[0], sz[1]
		capC, capR := max(1, W/minCellW), max(1, H/minCellH)
		for n := 0; n <= capC*capR; n++ {
			c, r := bestGrid(n, W, H, capC, capR)
			if c < 1 || r < 1 || c > capC || r > capR {
				t.Errorf("bestGrid(%d, %d, %d, %d, %d) = %d×%d outside 1..%d×%d", n, W, H, capC, capR, c, r, capC, capR)
			}
			if c*r < n {
				t.Errorf("bestGrid(%d, %d, %d) = %d×%d holds fewer than n", n, W, H, c, r)
			}
		}
	}
}

func TestBestGridShapes(t *testing.T) {
	cases := []struct {
		name                string
		n, W, H, capC, capR int
		cols, rows          int
	}{
		{"none", 0, 190, 47, 4, 5, 1, 1},
		{"one", 1, 190, 47, 4, 5, 1, 1},
		{"full page", 20, 190, 47, 4, 5, 4, 5},
		{"very wide", 4, 400, 20, 10, 2, 4, 1},
		{"very tall", 4, 40, 200, 1, 22, 1, 4},
	}
	for _, c := range cases {
		cols, rows := bestGrid(c.n, c.W, c.H, c.capC, c.capR)
		if cols != c.cols || rows != c.rows {
			t.Errorf("%s: got %d×%d, want %d×%d", c.name, cols, rows, c.cols, c.rows)
		}
	}
}

func TestGridLayoutPages(t *testing.T) {
	// 120 wide holds 3 cards across; 2 header + 18 body + 1 footer rows hold 2 down: 6 per page.
	w, h := 120, headerRows+2*minCellH+footerRows
	l := computeLayout(w, h, 14, 0, gridView, false, 8, 0)
	if l.perPage != 6 || l.page != 1 || l.pages != 3 || l.first != 6 || l.count != 6 {
		t.Fatalf("page of tile 8: %+v", l)
	}
	l = computeLayout(w, h, 14, 0, gridView, false, 13, 0)
	if l.page != 2 || l.first != 12 || l.count != 2 {
		t.Fatalf("last page: %+v", l)
	}
	if l.cols*l.rows < l.count {
		t.Fatalf("grid %d×%d too small for %d", l.cols, l.rows, l.count)
	}
	if i, ok := l.onPage(2); !ok || i != 13 {
		t.Errorf("card 2 on last page = %d, %v", i, ok)
	}
	if _, ok := l.onPage(3); ok {
		t.Error("card 3 exists on a page of 2")
	}
	z := computeLayout(w, h, 14, 0, gridView, true, 8, 0)
	if z.perPage != 1 || z.first != 8 || z.count != 1 || z.cols != 1 || z.rows != 1 {
		t.Errorf("zoom: %+v", z)
	}
	empty := computeLayout(w, h, 0, 0, gridView, false, -1, 0)
	if empty.count != 0 || empty.pages != 0 {
		t.Errorf("empty: %+v", empty)
	}
}

func TestCellsTileTheBody(t *testing.T) {
	l := computeLayout(190, 50, 7, 0, gridView, false, 0, 0)
	covered := 0
	for k := 0; k < l.count; k++ {
		c := l.cell(k)
		if c.x < 0 || c.x+c.w > l.w || c.y < l.bodyTop || c.y+c.h > l.bodyTop+l.bodyH {
			t.Errorf("cell %d %+v outside the body", k, c)
		}
		covered += c.w * c.h
	}
	if covered > l.w*l.bodyH {
		t.Errorf("cells overlap: %d cells of area for a %d body", covered, l.w*l.bodyH)
	}
}

func testSessions(n int) []model.Session {
	ss := make([]model.Session, n)
	for i := range ss {
		ss[i] = model.Session{ID: fmt.Sprintf("id%02d", i), Agent: "claude", Name: fmt.Sprintf("sess%02d", i), State: model.Idle, Since: time.Now()}
	}
	return ss
}

func TestListPageSizeIsVisibleRows(t *testing.T) {
	a := newTestApp(listView, 140, 40)
	a.snap.Sessions = testSessions(60)
	a.rebuild()
	a.selectIndex(30)
	a.relayout()
	lines, _ := a.compose()
	shown := 0
	for _, l := range lines {
		if row := ansi.Strip(l); strings.Contains(row, "sess") && !strings.Contains(row, "session") { // skip the details
			shown++
		}
	}
	if shown != a.lay.perPage || a.lay.count != a.lay.perPage {
		t.Fatalf("list shows %d rows, page size %d, count %d", shown, a.lay.perPage, a.lay.count)
	}
	if len(lines) != a.h {
		t.Fatalf("frame has %d lines for a %d-row window", len(lines), a.h)
	}
}

func TestDigitSelectsOnCurrentPage(t *testing.T) {
	for _, view := range []viewKind{gridView, listView} {
		a := newTestApp(view, 120, 30)
		a.snap.Sessions = testSessions(60)
		a.rebuild()
		a.relayout()
		a.selectIndex(a.lay.perPage + 1) // second page
		a.relayout()
		if a.lay.page != 1 {
			t.Fatalf("view %v: not on page 2: %+v", view, a.lay)
		}
		var d keyDecoder
		a.onKeys(d.feed([]byte("3")))
		if got, want := a.cur(), a.lay.first+2; got != want {
			t.Errorf("view %v: 3 selected %d, want %d", view, got, want)
		}
	}
}

func TestRailTakesTheRightSide(t *testing.T) {
	l := computeLayout(200, 50, 10, 4, gridView, false, 0, 0)
	if l.rail.w != 44 || l.gridW != 156 || l.mainN != 6 || l.count != 6 {
		t.Fatalf("layout: %+v", l)
	}
	if c := l.cell(l.count - 1); c.x+c.w != l.gridW {
		t.Errorf("last card ends at %d, grid is %d wide", c.x+c.w, l.gridW)
	}
	if !l.inRail(6) || l.inRail(5) {
		t.Errorf("inRail wrong around mainN")
	}
	// Selecting in the rail keeps the grid on the page it was on.
	if p := computeLayout(200, 12, 40, 4, gridView, false, 38, 2); p.page != 2 {
		t.Errorf("page with the selection in the rail: %d", p.page)
	}
	for _, narrow := range []layout{
		computeLayout(100, 50, 10, 4, gridView, false, 0, 0),
		computeLayout(200, 50, 10, 4, gridView, true, 0, 0),
		computeLayout(200, 50, 10, 4, listView, false, 0, 0),
	} {
		if narrow.rail.w != 0 || narrow.mainN != 10 {
			t.Errorf("no rail expected: %+v", narrow)
		}
	}
}

func TestMoveBetweenGridAndRail(t *testing.T) {
	a := newTestApp(gridView, 200, 50)
	ss := testSessions(6)
	for i := 4; i < 6; i++ {
		ss[i].Since = time.Now().Add(-3 * time.Hour)
	}
	a.snap.Sessions = ss
	a.rebuild()
	a.relayout()
	if a.lay.mainN != 4 || a.lay.rail.w == 0 {
		t.Fatalf("layout: %+v", a.lay)
	}
	a.selectIndex(a.lay.cols - 1)
	from := a.sel
	a.move(1, 0)
	if !a.lay.inRail(a.cur()) {
		t.Fatalf("right from the last column did not enter the rail: %d", a.cur())
	}
	a.move(0, 1)
	if a.cur() != 5 {
		t.Errorf("down in the rail: %d", a.cur())
	}
	a.move(0, 1)
	if a.cur() != 5 {
		t.Errorf("down past the rail's end: %d", a.cur())
	}
	a.move(-1, 0)
	if a.sel != from {
		t.Errorf("left returned to %s, want %s", a.sel, from)
	}
	a.move(1, 0)
	if a.cur() != 5 {
		t.Errorf("rail did not remember its row: %d", a.cur())
	}
}
