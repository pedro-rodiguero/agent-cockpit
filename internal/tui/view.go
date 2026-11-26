package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// Drawing reads App state and the pane client's screens and does nothing else: no I/O, no state
// changes. Everything it needs was prepared by the event loop (tiles, layout, conversations).

func ago(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 3*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// since is when the tile's current state began.
func since(t tile) time.Time {
	if t.sess != nil {
		if !t.sess.Since.IsZero() {
			return t.sess.Since
		}
		return t.sess.LastActivity
	}
	if t.pane != nil {
		return t.pane.Started
	}
	return time.Time{}
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func (a *App) draw() {
	lines, cur := a.compose()
	a.highlight(lines)
	a.scr.frame(lines, a.w, a.h, cur)
}

func (a *App) compose() ([]string, cursor) {
	w, h := a.w, a.h
	if w < minWindowCols || h < minWindowRows {
		return []string{paint(cMuted, cBg, fit("cockpit: window too small", w))}, cursor{}
	}
	lines := a.header(w)
	var body []string
	var cur cursor
	switch {
	case a.help:
		body = helpBody(w, h-headerRows-footerRows)
	case a.doc != nil:
		body = a.docBody(w, h-headerRows-footerRows)
	case a.view == gridView:
		body, cur = a.gridBody()
	default:
		body = a.listBody()
	}
	lines = append(lines, body...)
	for len(lines) < h-footerRows {
		lines = append(lines, pad("", w, cBg))
	}
	lines = lines[:h-footerRows]
	foot, fcur := a.footer(w, h-footerRows)
	if fcur.show {
		cur = fcur
	}
	return append(lines, foot), cur
}

// emptyMessage explains an empty body.
func (a *App) emptyMessage() string {
	switch {
	case a.loading:
		return "  collecting…"
	case a.onlyAttn:
		return "  nothing needs you right now · a shows all"
	case a.railN > 0:
		return "  nothing running right now · n opens a new pane"
	default:
		return "  no sessions · n opens a new one"
	}
}

func (a *App) header(w int) []string {
	title := paintBold(cGold, cBg, " ◆ AGENT COCKPIT")
	left := title + paint(cFaint, cBg, "  "+a.viewInfo())
	right := paint(cMuted, cBg, time.Now().Format("15:04:05")+" ")
	gap := max(1, w-width(left)-width(right))
	l1 := left + paint(cInk, cBg, strings.Repeat(" ", gap)) + right

	l2 := paint(cInk, cBg, " ")
	for _, st := range []model.State{model.Waiting, model.Asked, model.Stuck, model.Inbox, model.Busy, model.Idle} {
		l2 += a.stateChip(st)
	}
	switch {
	case a.mc != nil:
		live := livePanes(a.panes)
		plural := "s"
		if live == 1 {
			plural = ""
		}
		l2 += paint(cFaint, cBg, fmt.Sprintf("◧ %d pane%s", live, plural))
	case a.muxErr != "":
		l2 += paint(cStuck, cBg, "multiplexer off")
	}
	if a.syncErr != "" {
		l2 += paint(cStuck, cBg, "   ⇅ to-do sync failed (cockpit todo status)")
	}
	return []string{pad(l1, w, cBg), pad(l2, w, cBg)}
}

// viewInfo describes the view, the grid shape and the page in the header.
func (a *App) viewInfo() string {
	l := a.lay
	var info string
	switch {
	case a.view == gridView && a.zoom:
		info = "grid · zoom"
	case a.view == gridView:
		info = fmt.Sprintf("grid %d×%d", l.cols, l.rows)
	default:
		info = "list"
	}
	if l.pages > 1 && !a.zoom {
		info += fmt.Sprintf(" · page %d/%d", l.page+1, l.pages)
	}
	if a.onlyAttn {
		info += " · needs me only"
	}
	return info
}

// stateChip is the count of sessions in state st, dimmed when there are none.
func (a *App) stateChip(st model.State) string {
	l := look(st)
	n := a.snap.Count(st)
	fg, num := cFaint, cFaint
	if n > 0 {
		fg, num = cMuted, l.color
	}
	return paint(num, cBg, fmt.Sprintf("%s %d", l.glyph, n)) + paint(fg, cBg, " "+l.label+"   ")
}

// ---------- grid ----------

func (a *App) gridBody() ([]string, cursor) {
	l := a.lay
	var cur cursor
	var out []string
	if l.count == 0 {
		out = append(out, pad(paint(cMuted, cBg, a.emptyMessage()), l.gridW, cBg))
	}
	sel := a.cur()
	for r := 0; r < l.rows && l.count > 0; r++ {
		var cells [][]string
		rowH, used := l.cell(r*l.cols).h, 0
		for c := 0; c < l.cols; c++ {
			k := r*l.cols + c
			if k >= l.count {
				break
			}
			box := l.cell(k)
			i := l.first + k
			lines, cc := a.renderTile(a.tiles[i], box.w, box.h, i == sel, k+1)
			if cc.show {
				cur = cursor{x: box.x + cc.x, y: box.y + cc.y, show: true}
			}
			cells = append(cells, lines)
			used += box.w
		}
		for y := 0; y < rowH; y++ {
			var sb strings.Builder
			for _, cell := range cells {
				sb.WriteString(cell[y])
			}
			if used < l.gridW {
				sb.WriteString(paint(cInk, cBg, strings.Repeat(" ", l.gridW-used)))
			}
			out = append(out, sb.String())
		}
	}
	for len(out) < l.bodyH {
		out = append(out, pad("", l.gridW, cBg))
	}
	if l.rail.w > 0 {
		rail := a.railBody(l.rail, time.Now())
		for y := range out {
			out[y] += rail[y]
		}
	}
	return out, cur
}

func tailN(s []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// ---------- list ----------

// List view columns, in cells. The name takes what the others leave.
const (
	colMark, colGlyph, colState, colDir, colAgent, colWhere = 1, 2, 15, 26, 14, 7
	minListNameWidth                                        = 10
	maxListNameWidth                                        = 64 // past this the other columns drift too far from the name
)

// listBody is the list of tiles above a reading pane for the selected one.
func (a *App) listBody() []string {
	l := a.lay
	w := l.w
	var out []string
	add := func(s string) { out = append(out, pad(s, w, cBg)) }
	now := time.Now()
	wName := min(maxListNameWidth, max(minListNameWidth, w-(colMark+colGlyph+colState+colDir+colAgent+colWhere)-1))
	add(paint(cLine, cBg, strings.Repeat("─", w)))
	add(paint(cFaint, cBg, " "+fit("", colMark+colGlyph-1)+fit("SESSION", wName)+fit("STATE", colState)+
		fit("DIR", colDir)+fit("AGENT", colAgent)+fit("WHERE", colWhere)))
	if len(a.tiles) == 0 {
		add(paint(cMuted, cBg, a.emptyMessage()))
	}
	sel := a.cur()
	for i := l.first; i < l.first+l.count; i++ {
		add(listRow(a.tiles[i], i == sel, wName, now))
	}
	for len(out) < listHeadRows+l.perPage {
		add("")
	}
	add(paint(cLine, cBg, strings.Repeat("─", w)))
	if t, ok := a.selected(); ok {
		h := l.bodyH - len(out)
		var pane []string
		if t.pane != nil && a.mc != nil {
			pane, _ = a.paneContent(t, w, h, false)
		} else if t.sess != nil {
			pane = a.sessionContent(t, w, h)
		}
		for _, row := range pane {
			add(row)
		}
	}
	return out
}

func listRow(t tile, selected bool, wName int, now time.Time) string {
	bg, mark := cBg, " "
	if selected {
		bg, mark = cPanel, "▌"
	}
	st := t.state()
	l := look(st)
	nameFg, stateFg := cSoft, cFaint
	if st.NeedsAttention() || st == model.Busy {
		nameFg, stateFg = cInk, l.color
	}
	if selected {
		nameFg = cGoldHi
	}
	state := l.label + " " + ago(since(t), now)
	dir, where := "", ""
	if t.sess != nil {
		dir, where = tilde(t.sess.Cwd), strings.TrimPrefix(t.sess.TTY, "tty")
	}
	if t.pane != nil {
		dir, where = tilde(t.pane.Cwd), "◧ pane"
		if t.pane.Exited {
			state = "ended"
		}
	}
	if t.isItem() {
		state, dir = firstOf(t.sess.Detail, "item")+" "+ago(since(t), now), ""
	}
	agent := t.agent()
	if t.sess != nil && t.sess.Profile != "" {
		agent += " " + t.sess.Profile
	}
	return paint(cGold, bg, mark) + paint(l.color, bg, fit(l.glyph, colGlyph)) +
		sgr(nameFg, bg, selected) + fit(t.name(), wName) +
		paint(stateFg, bg, fit(state, colState)) +
		paint(cMuted, bg, fit(fitLeft(dir, colDir-1), colDir)) + // one cell of gap before the next column
		paint(cMuted, bg, fit(agent, colAgent)) + paint(cFaint, bg, fit(where, colWhere))
}

// ---------- footer ----------

func (a *App) footer(w, y int) (string, cursor) {
	if a.mode == promptMode && a.prompt != nil {
		return a.promptLine(w, y)
	}
	if a.flash.alert && time.Now().Before(a.flash.until) {
		return pad(paint(cWaiting, cPanel, " "+a.flash.text+"   esc dismisses"), w, cPanel), cursor{}
	}
	key := func(k, d string) string { return paint(cGold, cBg, k) + paint(cFaint, cBg, " "+d+"   ") }
	foot := paint(cInk, cBg, " ")
	switch {
	case a.mode == insertMode:
		foot += paint(cGoldHi, cBg, "typing into the agent   ") + key("ctrl-q", "back to cockpit")
		if a.zoom {
			foot += key("esc", "all panes")
		} else {
			foot += key("ctrl-z", "zoom")
		}
	case a.help:
		foot += key("any key", "closes this")
	case a.doc != nil:
		foot += a.docFooterKeys(key)
	default:
		foot += a.footerKeys(w, key)
	}
	if msg := a.footerMessage(); msg != "" {
		if room := w - width(foot) - 1; room > 8 {
			foot += paint(cMuted, cBg, fit(msg, room))
		}
	}
	return pad(foot, w, cBg), cursor{}
}

// footerKeys are the keys for the selected tile, then the general ones that fit in w cells. "? keys"
// always stays, since it leads to the rest.
func (a *App) footerKeys(w int, key func(k, d string) string) string {
	var own string
	if t, ok := a.selected(); ok {
		for _, k := range tileKeys(t) {
			own += key(k.key, k.does)
		}
		if a.hasPlan(t) {
			own += key("m", "plan")
		}
		own += paint(cLine, cBg, "│  ")
	}
	otherView := "grid"
	if a.view == gridView {
		otherView = "list"
	}
	general := []keyHint{{"n", "new pane"}, {"+", "task"}, {"!", "next needing me"}, {"v", otherView}, {"q", "quit"}}
	tail := key("?", "keys")
	out := own
	for _, k := range general {
		if s := key(k.key, k.does); width(out+s+tail) < w {
			out += s
		}
	}
	return out + tail
}

// footerMessage is the current flash, or else the sources' errors.
func (a *App) footerMessage() string {
	if a.flash.text != "" && time.Now().Before(a.flash.until) {
		return a.flash.text
	}
	var errs []string
	for k, v := range a.snap.Errors {
		errs = append(errs, k+": "+v)
	}
	sort.Strings(errs)
	return strings.Join(errs, " · ")
}

// matchList shows the directories the typed path completes to in room cells, the one tab put in the
// field highlighted and always in view.
func matchList(pr *prompt, room int) string {
	item := func(i int) string {
		name := matchName(pr.matches[i])
		if pr.cycling && i == pr.at {
			return paintBold(cBg, cGold, " "+name+" ")
		}
		return paint(cSoft, cPanel, " "+name+" ")
	}
	n, sel := len(pr.matches), 0
	if pr.cycling {
		sel = pr.at
	}
	more := func(k int) string {
		if k == 0 {
			return ""
		}
		return paint(cFaint, cPanel, fmt.Sprintf(" +%d", k))
	}
	for from := 0; from <= sel; from++ {
		out := paint(cFaint, cPanel, "  ")
		if from > 0 {
			out += paint(cFaint, cPanel, "‹")
		}
		to := from
		for to < n {
			next := out + item(to)
			if width(next+more(n-to-1)) > room {
				break
			}
			out, to = next, to+1
		}
		if to > sel || (to == from && from == sel) {
			return out + more(n-to)
		}
	}
	return ""
}

func (a *App) promptLine(w, y int) (string, cursor) {
	pr := a.prompt
	var line string
	var cur cursor
	switch pr.kind {
	case promptNew:
		line = paint(cGold, cPanel, " new ▸ ")
		for i, l := range a.launchers {
			if i == pr.agent {
				line += paintBold(cBg, cGold, " "+l.label+" ") + paint(cInk, cPanel, " ")
			} else {
				line += paint(cMuted, cPanel, " "+l.label+"  ")
			}
		}
		line += paint(cGold, cPanel, "  dir ▸ ") + paint(cInk, cPanel, string(pr.text))
		cur = cursor{x: width(line), y: y, show: true}
		line += paint(cFaint, cPanel, pr.ghost())
		switch {
		case pr.listErr != "":
			line += paint(cStuck, cPanel, "   "+pr.listErr)
		case len(pr.matches) > 1:
			line += matchList(pr, w-width(line))
		default:
			line += paint(cFaint, cPanel, "   ←→ agent · tab complete · ⏎ start · esc cancel")
		}
	case promptReply, promptTask, promptFeedback:
		line = paint(cGold, cPanel, " "+pr.label+" ▸ ")
		// New lines show as ↵, and a text longer than the room shows its end, where the cursor is.
		text := strings.ReplaceAll(string(pr.text), "\n", "↵")
		if room := w - width(line) - 1; width(text) > room && room > 1 {
			text = "…" + ansi.TruncateLeft(text, width(text)-(room-1), "")
		}
		line += paint(cInk, cPanel, text)
		cur = cursor{x: width(line), y: y, show: true}
		hint := "   ⏎ send · alt-⏎ new line · esc cancel"
		if pr.kind == promptTask {
			hint = "   #group @date (today, tomorrow, 25/12) · ⏎ save · esc cancel"
		}
		if width(line)+width(hint) <= w {
			line += paint(cFaint, cPanel, hint)
		}
	case promptConfirm:
		line = paint(cWaiting, cPanel, " "+pr.label)
	}
	if cur.x >= w {
		cur.show = false
	}
	return pad(line, w, cPanel), cur
}
