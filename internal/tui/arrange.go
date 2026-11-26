package tui

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

// The person arranges the grid: H J K L (or shift and an arrow) move the selected card, > and < make it
// bigger or smaller, = makes every card the same size again, and the mouse drags a card by its title
// onto another or drags the border between two cards. The grid's shape still follows the number of
// cards; what the person sets is the order they fill it in and the share of room each one gets.

// cardSize is how much of its row's width a card asks for, and how much height for its row, relative
// to the 1 every card starts with. A row is as tall as the mean of its cards' heights asks.
type cardSize struct {
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var defaultSize = cardSize{1, 1}

const (
	growStep = 1.4  // > multiplies a card's size by this; < divides
	minShare = 0.25 // the smallest and largest size a card can ask for
	maxShare = 4.0
)

func (c cardSize) clamped() cardSize {
	return cardSize{math.Max(minShare, math.Min(maxShare, c.W)), math.Max(minShare, math.Min(maxShare, c.H))}
}

// arrangement is the order and sizes the person gave the grid's cards, in arrangement.json next to
// the to-do list, keyed by tile identity.
type arrangement struct {
	path  string
	Order []string            `json:"order,omitempty"`
	Sizes map[string]cardSize `json:"sizes,omitempty"`
}

func loadArrangement(path string) *arrangement {
	r := &arrangement{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, r)
	}
	if r.Sizes == nil {
		r.Sizes = map[string]cardSize{}
	}
	return r
}

// positions maps each arranged identity to its place in the order.
func (r *arrangement) positions() map[string]int {
	if len(r.Order) == 0 {
		return nil
	}
	m := make(map[string]int, len(r.Order))
	for i, id := range r.Order {
		m[id] = i
	}
	return m
}

func (r *arrangement) size(id string) cardSize {
	if s, ok := r.Sizes[id]; ok {
		return s
	}
	return defaultSize
}

// save writes the file whole, through a temporary file, forgetting cards that no longer exist.
func (r *arrangement) save(tiles []tile) {
	live := map[string]bool{}
	for _, t := range tiles {
		live[t.identity()] = true
	}
	order := r.Order[:0:0]
	for _, id := range r.Order {
		if live[id] {
			order = append(order, id)
		}
	}
	r.Order = order
	for id, s := range r.Sizes {
		if !live[id] || s == defaultSize {
			delete(r.Sizes, id)
		}
	}
	if r.path == "" {
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(r.path), 0o700) != nil {
		return
	}
	tmp := r.path + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
		_ = os.Rename(tmp, r.path)
	}
}

// sizeOf is the size of each tile, by index, for the layout.
func (a *App) sizeOf(i int) cardSize {
	if a.arr == nil || i < 0 || i >= len(a.tiles) {
		return defaultSize
	}
	return a.arr.size(a.tiles[i].identity())
}

// gridArranging reports whether the grid's cards can be arranged now, and says why not when they
// cannot.
func (a *App) gridArranging() bool {
	switch {
	case a.arr == nil:
		return false
	case a.view != gridView || a.zoom:
		a.say("cards are arranged in the grid view, out of zoom")
		return false
	}
	i := a.cur()
	return i < a.lay.mainN && len(a.tiles) > 0
}

// moveCard swaps the selected card with its neighbour in the grid in direction (dx, dy).
func (a *App) moveCard(dx, dy int) {
	if !a.gridArranging() {
		return
	}
	i := a.cur()
	j := i + dx
	if dy != 0 {
		j = i + dy*a.lay.cols
	}
	if j < 0 || j >= a.lay.mainN {
		return
	}
	a.swapCards(i, j)
}

// swapCards exchanges the places of grid cards i and j and remembers the new order.
func (a *App) swapCards(i, j int) {
	if i == j || a.arr == nil {
		return
	}
	order := make([]string, 0, a.lay.mainN)
	for _, t := range a.tiles[:a.lay.mainN] {
		order = append(order, t.identity())
	}
	order[i], order[j] = order[j], order[i]
	// Arranged cards that are in the rail now keep their places for when they come back.
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	for _, id := range a.arr.Order {
		if !seen[id] {
			order = append(order, id)
		}
	}
	a.arr.Order = order
	a.arr.save(a.tiles)
	a.rebuild()
}

// growCard multiplies the selected card's size by f.
func (a *App) growCard(f float64) {
	if !a.gridArranging() {
		return
	}
	id := a.tiles[a.cur()].identity()
	s := a.arr.size(id)
	a.arr.Sizes[id] = cardSize{s.W * f, s.H * f}.clamped()
	a.arr.save(a.tiles)
}

// resetSizes gives every card the same size again.
func (a *App) resetSizes() {
	if a.arr == nil {
		return
	}
	a.arr.Sizes = map[string]cardSize{}
	a.arr.save(a.tiles)
	a.say("every card the same size again")
}

// cardDrag is a card being moved or a border between cards being dragged with the mouse.
type cardDrag struct {
	kind dragKind
	k    int // the card on the page: the one moved, or the one left of or above the border
}

type dragKind int

const (
	dragMove   dragKind = iota // by the title, onto another card
	dragWidth                  // the border between card k and the next one in its row
	dragHeight                 // the border between card k's row and the row below
)

// dragAt says what pressing the button at (x, y) would drag, if anything: the title row of a card moves
// it, the border between two cards in a row resizes them, and the bottom border of a row resizes it
// against the row below.
func (a *App) dragAt(x, y int) (*cardDrag, bool) {
	l := a.lay
	if a.arr == nil || a.view != gridView || a.zoom || a.help {
		return nil, false
	}
	for k := 0; k < l.count; k++ {
		c := l.cell(k)
		if !c.contains(x, y) {
			continue
		}
		row := k / l.cols
		sameRow := k+1 < l.count && (k+1)/l.cols == row
		switch {
		case x == c.x+c.w-1 && sameRow && y > c.y:
			return &cardDrag{kind: dragWidth, k: k}, true
		case x == c.x && k > 0 && (k-1)/l.cols == row && y > c.y:
			return &cardDrag{kind: dragWidth, k: k - 1}, true
		case y == c.y+c.h-1 && row < l.rows-1:
			return &cardDrag{kind: dragHeight, k: k}, true
		case y == c.y:
			return &cardDrag{kind: dragMove, k: k}, true
		}
		return nil, false
	}
	return nil, false
}

// startDrag starts moving or resizing cards when the button goes down on a title or a border.
func (a *App) startDrag(x, y int) bool {
	d, ok := a.dragAt(x, y)
	if ok {
		a.drag = d
	}
	return ok
}

// dragTo follows the pointer to (x, y) while a border is dragged.
func (a *App) dragTo(x, y int) {
	d, l := a.drag, a.lay
	switch d.kind {
	case dragMove:
		// Nothing moves until the card is dropped.
	case dragWidth:
		left, right := l.cell(d.k), l.cell(d.k+1)
		total := left.w + right.w
		if total < 2*minCellW {
			return
		}
		wl := max(min(x+1-left.x, total-minCellW), minCellW)
		il, ir := l.first+d.k, l.first+d.k+1
		sl, sr := a.sizeOf(il), a.sizeOf(ir)
		sum := sl.W + sr.W
		sl.W = sum * float64(wl) / float64(total)
		sr.W = sum - sl.W
		a.arr.Sizes[a.tiles[il].identity()], a.arr.Sizes[a.tiles[ir].identity()] = sl, sr
	case dragHeight:
		row := d.k / l.cols
		top, below := l.cell(row*l.cols), l.cell((row+1)*l.cols)
		total := top.h + below.h
		if total < 2*minCellH {
			return
		}
		ht := max(min(y+1-top.y, total-minCellH), minCellH)
		mt, mb := a.rowMean(row), a.rowMean(row+1)
		sum := mt + mb
		a.scaleRow(row, sum*float64(ht)/float64(total)/mt)
		a.scaleRow(row+1, (sum-sum*float64(ht)/float64(total))/mb)
	}
}

// rowMean is the mean height the cards of row asks for.
func (a *App) rowMean(row int) float64 {
	l := a.lay
	from, to := l.first+row*l.cols, min(l.first+l.count, l.first+(row+1)*l.cols)
	sum := 0.0
	for i := from; i < to; i++ {
		sum += a.sizeOf(i).H
	}
	return sum / float64(max(1, to-from))
}

func (a *App) scaleRow(row int, f float64) {
	l := a.lay
	for i := l.first + row*l.cols; i < min(l.first+l.count, l.first+(row+1)*l.cols); i++ {
		s := a.sizeOf(i)
		s.H *= f
		a.arr.Sizes[a.tiles[i].identity()] = s
	}
}

// endDrag finishes a drag at (x, y): a card dropped on another swaps places with it, and a resize is
// saved. A title clicked without moving selects its card, like any other click.
func (a *App) endDrag(x, y int) {
	d := a.drag
	a.drag = nil
	if d.kind != dragMove {
		a.arr.save(a.tiles)
		return
	}
	from := a.lay.first + d.k
	if _, to := a.regionAt(x, y); to >= 0 && to != from && to < a.lay.mainN {
		a.swapCards(from, to)
		a.sel = a.tiles[to].key
		return
	}
	if a.mode == navMode {
		a.selectIndex(from)
	}
}

// splitShares divides total cells among weights, giving each at least least cells when there is room.
func splitShares(total int, weights []float64, least int) []int {
	n := len(weights)
	out := make([]int, n)
	if n == 0 {
		return out
	}
	least = min(least, total/n)
	fixed := make([]bool, n)
	for {
		rest, sum := total, 0.0
		for i, w := range weights {
			if fixed[i] {
				rest -= least
			} else {
				sum += w
			}
		}
		acc, prev := 0.0, 0
		for i, w := range weights {
			if fixed[i] {
				out[i] = least
				continue
			}
			acc += w
			end := int(math.Round(acc / sum * float64(rest)))
			out[i], prev = end-prev, end
		}
		changed := false
		for i := range out {
			if !fixed[i] && out[i] < least {
				fixed[i], changed = true, true
			}
		}
		if !changed {
			return out
		}
	}
}

// arrange gives the cards on the page the sizes the person set; size is by tile index. When every card
// has the default size the layout keeps its even grid.
func (l *layout) arrange(size func(i int) cardSize) {
	l.cells = nil
	if l.view != gridView || l.count == 0 {
		return
	}
	even := true
	for k := 0; k < l.count; k++ {
		if size(l.first+k) != defaultSize {
			even = false
			break
		}
	}
	if even {
		return
	}
	rowW := make([]float64, l.rows)
	for r := range rowW {
		n, sum := 0, 0.0
		for k := r * l.cols; k < min(l.count, (r+1)*l.cols); k++ {
			sum += size(l.first + k).H
			n++
		}
		rowW[r] = sum / float64(max(1, n))
	}
	heights := splitShares(l.bodyH, rowW, minCellH)
	y := l.bodyTop
	for r := range rowW {
		from, to := r*l.cols, min(l.count, (r+1)*l.cols)
		ws := make([]float64, 0, to-from)
		for k := from; k < to; k++ {
			ws = append(ws, size(l.first+k).W)
		}
		x := 0
		for _, w := range splitShares(l.gridW, ws, minCellW) {
			l.cells = append(l.cells, rect{x: x, y: y, w: w, h: heights[r]})
			x += w
		}
		y += heights[r]
	}
}
