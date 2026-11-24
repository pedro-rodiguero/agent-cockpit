package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// Drawing reads App state and the pane client's screens and does nothing else: no I/O, no state
// changes. Everything it needs was prepared by the event loop (tiles, layout, conversations).

const (
	kvLabelWidth = 11 // the key column of the list view's details
)

// List view columns, in cells.
const (
	colState, colAgent, colName, colDir, colAge, colTTY = 3, 9, 22, 28, 7, 9
	minSubjectWidth                                     = 10
)

// ago formats how long ago t was as a duration that cannot be read as a clock time: "45s", "12m",
// "2h05m", "19h", "3d". Minutes stop mattering after a few hours.
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

func (a *App) listBody() []string {
	l := a.lay
	w := l.w
	var out []string
	add := func(s string) { out = append(out, pad(s, w, cBg)) }
	now := time.Now()
	wSubj := max(minSubjectWidth, w-(colState+colAgent+colName+colDir+colAge+colTTY)-2)
	add(paint(cLine, cBg, strings.Repeat("─", w)))
	add(paint(cFaint, cBg, " "+fit("", colState)+fit("AGENT", colAgent)+fit("SESSION", colName)+fit("DIR", colDir)+
		fit("FOR", colAge)+fit("TTY", colTTY)+fit("SUBJECT", wSubj)))
	if len(a.tiles) == 0 {
		add(paint(cMuted, cBg, a.emptyMessage()))
	}
	sel := a.cur()
	for i := l.first; i < l.first+l.count; i++ {
		add(listRow(a.tiles[i], i == sel, wSubj, now))
	}
	for len(out) < l.bodyH-listDetailRows {
		add("")
	}
	add(paint(cLine, cBg, strings.Repeat("─", w)))
	return append(out, a.listDetails(w, now)...)
}

func listRow(t tile, selected bool, wSubj int, now time.Time) string {
	bg, mark := cBg, " "
	if selected {
		bg, mark = cPanel, "▌"
	}
	st := t.state()
	l := look(st)
	nameFg, subjFg, ageFg := cSoft, cMuted, cFaint
	if st.NeedsAttention() {
		nameFg, subjFg, ageFg = cInk, cSoft, l.color
	}
	if selected {
		nameFg = cGoldHi
	}
	dir, tty, subj := "", "", ""
	if t.sess != nil {
		dir, tty, subj = tilde(t.sess.Cwd), strings.TrimPrefix(t.sess.TTY, "tty"), listSubject(*t.sess)
	}
	if t.pane != nil {
		dir, tty = tilde(t.pane.Cwd), "◧ pane"
	}
	agent := t.agent()
	if t.sess != nil && t.sess.Profile != "" {
		agent += "²"
	}
	return paint(cGold, bg, mark) + paint(l.color, bg, fit(l.glyph, colState)) + paint(cMuted, bg, fit(agent, colAgent)) +
		sgr(nameFg, bg, selected) + fit(t.name(), colName) +
		paint(cMuted, bg, fit(fitLeft(dir, colDir-1), colDir)) + // one cell of gap before the next column
		paint(ageFg, bg, fit(ago(since(t), now), colAge)) + paint(cFaint, bg, fit(tty, colTTY)) + paint(subjFg, bg, fit(subj, wSubj))
}

// listSubject is what a session is about, led by why it needs the person when it does.
func listSubject(s model.Session) string {
	subj := s.Title
	if s.State.NeedsAttention() && s.Detail != "" {
		subj = s.Detail + " · " + subj
	}
	return firstOf(subj, s.LastPrompt)
}

// listDetails describes the selected tile below the list.
func (a *App) listDetails(w int, now time.Time) []string {
	var out []string
	row := func(label, value string, fg rgb) {
		out = append(out, pad(paint(cGold, cBg, fit("  "+label, kvLabelWidth))+paint(fg, cBg, fit(value, w-kvLabelWidth)), w, cBg))
	}
	t, ok := a.selected()
	switch {
	case !ok:
		return nil
	case t.sess == nil:
		row("pane", t.name()+"   "+t.pane.Agent+fmt.Sprintf(" · pid %d", t.pane.PID), cInk)
		row("dir", tilde(t.pane.Cwd), cSoft)
		return out
	}
	s := t.sess
	meta := []string{s.Agent}
	if s.Profile != "" {
		meta = append(meta, "profile "+s.Profile)
	}
	if s.PID != 0 {
		meta = append(meta, fmt.Sprintf("pid %d", s.PID))
	}
	if t.pane != nil {
		meta = append(meta, "cockpit pane")
	} else if s.TTY != "" {
		meta = append(meta, s.TTY)
	}
	info := a.convs[s.ID].Info
	for _, v := range []string{info.Model, info.Mode, info.Effort} {
		if v != "" {
			meta = append(meta, v)
		}
	}
	if ctx, _ := contextSize(info); ctx != "" {
		meta = append(meta, ctx)
	}
	row("session", s.Name+"   "+strings.Join(meta, " · "), cInk)

	stateLine := look(s.State).label + " · for " + ago(since(t), now)
	if detail := stateDetail(*s, info); detail != "" {
		stateLine += " · " + detail
	}
	row("state", stateLine, look(s.State).color)
	dir := firstOf(tilde(s.Cwd), "—")
	if info.Branch != "" {
		dir += "  " + branchGlyph + info.Branch
	}
	row("dir", dir, cSoft)
	row("subject", firstOf(s.Title, "—"), cSoft)

	promptLabel := "prompt"
	if s.State == model.Inbox {
		promptLabel = "text"
	}
	pr := wrap(s.LastPrompt, w-kvLabelWidth)
	row(promptLabel, firstOf(lineAt(pr, 0), "—"), cMuted)
	row("", lineAt(pr, 1), cMuted)
	if s.URL != "" {
		row("link", s.URL, cFaint)
	} else {
		row("log", firstOf(s.Log, "—"), cFaint)
	}
	return out
}

func lineAt(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
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
	case a.help:
		foot += key("any key", "closes this")
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
		own += paint(cLine, cBg, "│  ")
	}
	otherView := "grid"
	if a.view == gridView {
		otherView = "list"
	}
	general := []keyHint{{"n", "new"}, {"!", "next needing me"}, {"v", otherView}, {"q", "quit"}}
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
		line += paint(cFaint, cPanel, "   ↑↓ agent · tab complete · ⏎ start · esc cancel")
	case promptReply:
		line = paint(cGold, cPanel, " "+pr.label+" ▸ ") + paint(cInk, cPanel, string(pr.text))
		cur = cursor{x: width(line), y: y, show: true}
		line += paint(cFaint, cPanel, "   ⏎ send · esc cancel")
	case promptConfirm:
		line = paint(cWaiting, cPanel, " "+pr.label)
	}
	if cur.x >= w {
		cur.show = false
	}
	return pad(line, w, cPanel), cur
}
