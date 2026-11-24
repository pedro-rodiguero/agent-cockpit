package tui

// Screen structure, in rows and cells.
const (
	headerRows = 2 // title line and state counts
	footerRows = 1 // key hints, prompt or message

	minWindowCols = 20 // below this the dashboard only says the window is too small
	minWindowRows = 6

	minCellW = 40 // smallest grid card, borders included
	minCellH = 9

	// The side rail takes railPercent of the window, within these bounds, once the window is at least
	// minRailWindow wide; narrower windows show the rail's tiles as cards.
	railPercent   = 22
	minRailW      = 32
	maxRailW      = 46
	minRailWindow = 110

	listHeadRows   = 2 // separator and column titles
	listDetailRows = 8 // separator and the selected session's details
	minListRows    = 3
)

// cellAspect is how many times taller than wide a terminal cell is, roughly. bestGrid multiplies card
// heights by it so that "the biggest card" compares widths and heights in the same visual unit.
const cellAspect = 2.4

// gridTieEpsilon keeps bestGrid on the arrangement with fewer columns when two score the same up to
// rounding, so the grid does not flip between equivalent shapes.
const gridTieEpsilon = 0.01

type viewKind int

const (
	listView viewKind = iota
	gridView
)

// rect is a region of the screen in cells.
type rect struct{ x, y, w, h int }

// layout is where everything goes in one frame. It depends only on the window size, the number of tiles,
// the view and the selection, and is computed by the event loop before drawing.
type layout struct {
	view        viewKind
	w           int
	gridW       int  // width of the card grid: the window minus the rail
	rail        rect // the side rail; zero when there is none
	mainN       int  // tiles shown as cards or list rows; the ones after them are in the rail
	bodyTop     int  // first screen row of the body
	bodyH       int  // rows between header and footer
	cols, rows  int  // grid arrangement of the current page; the list is one column
	first       int  // index of the first tile on the current page
	count       int  // tiles on the current page
	perPage     int  // tiles on a full page: the paging step
	page, pages int
}

// computeLayout lays out n tiles, the last railN of which belong in the rail, with tile sel selected.
// When the selection is in the rail the grid stays on page.
func computeLayout(w, h, n, railN int, view viewKind, zoom bool, sel, page int) layout {
	l := layout{view: view, w: w, gridW: w, mainN: n, bodyTop: headerRows, bodyH: max(0, h-headerRows-footerRows), cols: 1, rows: 1}
	tiled := view == gridView && !zoom
	if tiled && railN > 0 && w >= minRailWindow {
		rw := min(maxRailW, max(minRailW, w*railPercent/100))
		l.gridW, l.mainN = w-rw, n-railN
		l.rail = rect{x: l.gridW, y: l.bodyTop, w: rw, h: l.bodyH}
	}
	capC, capR := max(1, l.gridW/minCellW), max(1, l.bodyH/minCellH)
	switch {
	case view == listView:
		l.perPage = listRows(l.bodyH)
	case zoom:
		l.perPage = 1
	default:
		l.perPage = capC * capR
	}
	if l.mainN > 0 {
		l.pages = (l.mainN + l.perPage - 1) / l.perPage
		if sel >= 0 && sel < l.mainN {
			l.page = sel / l.perPage
		} else {
			l.page = max(0, min(l.pages-1, page))
		}
		l.first = l.page * l.perPage
		l.count = min(l.mainN, l.first+l.perPage) - l.first
	}
	if tiled {
		l.cols, l.rows = bestGrid(l.count, l.gridW, l.bodyH, capC, capR)
	}
	return l
}

// inRail reports whether tile i is in the rail.
func (l layout) inRail(i int) bool { return l.rail.w > 0 && i >= l.mainN }

// listRows is how many session rows the list view shows in a body of bodyH rows.
func listRows(bodyH int) int { return max(minListRows, bodyH-listHeadRows-listDetailRows) }

// bestGrid picks the columns and rows for n cards in a W×H body, at most capC×capR, that make the
// cards as large as possible.
func bestGrid(n, W, H, capC, capR int) (cols, rows int) {
	if n <= 0 {
		return 1, 1
	}
	cols, rows = min(n, capC), (n+capC-1)/capC
	best := -1.0
	for c := 1; c <= min(n, capC); c++ {
		r := (n + c - 1) / c
		if r > capR {
			continue
		}
		score := min(float64(W/c), float64(H/r)*cellAspect)
		if score > best+gridTieEpsilon {
			cols, rows, best = c, r, score
		}
	}
	return max(1, cols), max(1, min(rows, capR))
}

// cell is the screen region of the k-th card on the page, borders included. A last row with fewer
// cards than columns spreads them over the whole width rather than leaving a hole.
func (l layout) cell(k int) rect {
	r, c := k/l.cols, k%l.cols
	cols := l.cols
	if r == l.rows-1 {
		cols = max(1, l.count-r*l.cols)
	}
	x0, x1 := c*l.gridW/cols, (c+1)*l.gridW/cols
	y0, y1 := r*l.bodyH/l.rows, (r+1)*l.bodyH/l.rows
	return rect{x: x0, y: l.bodyTop + y0, w: x1 - x0, h: y1 - y0}
}

// innerSize is the terminal size of a pane shown in the k-th card: the card minus its borders.
func (l layout) innerSize(k int) (cols, rows int) {
	c := l.cell(k)
	return max(1, c.w-2), max(1, c.h-2)
}

// onPage returns the index of the tile shown as number num (1-based) on the current page.
func (l layout) onPage(num int) (int, bool) {
	if num < 1 || num > l.count {
		return 0, false
	}
	return l.first + num - 1, true
}
