package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// Drawing reads App state and the pane client's screens and does nothing else: no I/O, no state
// changes. Everything it needs was prepared by the event loop (tiles, layout, conversations).

const (
	kvLabelWidth   = 11 // the key column of the list view's details
	minNameWidth   = 4  // a card title keeps at least this much of the name
	maxCardNumber  = 9  // cards beyond this have no digit key
	cardBorderCols = 2  // left and right border of a card
)

// List view columns, in cells.
const (
	colState, colAgent, colName, colDir, colAge, colTTY = 3, 9, 22, 28, 7, 9
	minSubjectWidth                                     = 10
)

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
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
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
	if a.view == gridView {
		body, cur = a.gridBody()
	} else {
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
	for _, st := range []model.State{model.Waiting, model.Stuck, model.Inbox, model.Busy, model.Idle} {
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
	if len(a.tiles) == 0 {
		return []string{pad(paint(cMuted, cBg, a.emptyMessage()), l.w, cBg)}, cur
	}
	sel := a.cur()
	out := make([]string, 0, l.bodyH)
	for r := 0; r < l.rows; r++ {
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
			if used < l.w {
				sb.WriteString(paint(cInk, cBg, strings.Repeat(" ", l.w-used)))
			}
			out = append(out, sb.String())
		}
	}
	return out, cur
}

func (a *App) renderTile(t tile, cw, ch int, selected bool, num int) ([]string, cursor) {
	iw, ih := cw-cardBorderCols, ch-cardBorderCols
	insert := selected && a.mode == insertMode
	border := cardBorder(t, selected, insert)

	lines := []string{a.cardTop(t, cw, border, selected, num)}
	var body []string
	var cur cursor
	if t.pane != nil {
		body, cur = a.paneContent(t, iw, ih, insert)
	} else {
		body = a.chatContent(t, iw, ih)
	}
	for i := 0; i < ih; i++ {
		row := pad("", iw, cBg)
		if i < len(body) {
			row = pad(body[i], iw, cBg)
		}
		lines = append(lines, paint(border, cBg, "│")+row+paint(border, cBg, "│"))
	}
	lines = append(lines, cardBottom(t, cw, border, selected, insert))
	if cur.show {
		cur.x++
		cur.y++
	}
	return lines, cur
}

func cardBorder(t tile, selected, insert bool) rgb {
	switch {
	case insert:
		return cGoldHi
	case selected:
		return cGold
	case t.state().NeedsAttention():
		return cLineAttn
	}
	return cLine
}

// cardTop is the top border with the title: ┌ ▲ name · agent ───── s006 · 2m · 1 ┐
func (a *App) cardTop(t tile, cw int, border rgb, selected bool, num int) string {
	st := t.state()
	nameFg := cSoft
	switch {
	case selected:
		nameFg = cGoldHi
	case st.NeedsAttention():
		nameFg = cInk
	}
	agent := t.agent()
	if t.pane != nil {
		agent += " ◧"
	}
	rightTxt := ago(since(t), time.Now())
	if t.sess != nil && t.sess.TTY != "" && t.pane == nil {
		rightTxt = strings.TrimPrefix(t.sess.TTY, "tty") + " · " + rightTxt
	}
	if num <= maxCardNumber && !a.zoom {
		rightTxt = fmt.Sprintf("%s · %d", rightTxt, num)
	}
	right := paint(cFaint, cBg, " "+rightTxt+" ")
	opening := paint(border, cBg, "┌ ") + paint(look(st).color, cBg, look(st).glyph+" ")
	agentPart := paint(cMuted, cBg, " · "+agent+" ")
	const minFill, corner = 1, 1
	nameRoom := max(minNameWidth, cw-width(opening)-width(agentPart)-width(right)-minFill-corner)
	left := opening + paintBold(nameFg, cBg, fit(t.name(), min(nameRoom, width(t.name())))) + agentPart
	fill := cw - corner - width(left) - width(right)
	if fill < minFill {
		right = ""
		fill = cw - corner - width(left)
	}
	return pad(left+paint(border, cBg, strings.Repeat("─", max(0, fill)))+right+paint(border, cBg, "┐"), cw, cBg)
}

func cardBottom(t tile, cw int, border rgb, selected, insert bool) string {
	hint, hfg := "", cGold
	if selected {
		hint = cardHint(t, insert)
	}
	if insert {
		hfg = cGoldHi
	}
	if width(hint) > cw-4 {
		hint = ""
	}
	bot := paint(border, cBg, "└") + paint(hfg, cBg, hint) + paint(border, cBg, strings.Repeat("─", max(0, cw-cardBorderCols-width(hint)))+"┘")
	return pad(bot, cw, cBg)
}

// cardHint lists the keys that work on the selected card.
func cardHint(t tile, insert bool) string {
	switch {
	case insert:
		return " typing into agent · ctrl-q leaves "
	case t.pane != nil && t.pane.Exited:
		return " exited · x close "
	case t.pane != nil:
		return " ⏎ type · z zoom · x close "
	case t.isItem():
		return " ⏎ open "
	}
	keys := []string{"⏎ go to tab"}
	if t.canAdopt() {
		keys = append(keys, "p to pane")
	}
	if t.canReply() {
		keys = append(keys, "r reply")
	}
	return " " + strings.Join(append(keys, "z zoom"), " · ") + " "
}

func (a *App) paneContent(t tile, iw, ih int, insert bool) ([]string, cursor) {
	var cur cursor
	pn := t.pane
	scr := a.mc.Screen(pn.ID)
	if scr == nil {
		return []string{paint(cMuted, cBg, " starting "+pn.Agent+"…")}, cur
	}
	body := make([]string, 0, ih)
	for i := 0; i < ih && i < len(scr.Lines); i++ {
		body = append(body, scr.Lines[i])
	}
	if pn.Exited {
		msg := fmt.Sprintf(" exited · code %d · x closes the pane ", pn.ExitCode)
		for len(body) < ih {
			body = append(body, "")
		}
		body[ih-1] = pad(paint(cStuck, cPanel, msg), iw, cPanel)
	}
	if insert && scr.Cursor && scr.CX < iw && scr.CY < ih {
		cur = cursor{x: scr.CX, y: scr.CY, show: true}
	}
	return body, cur
}

// chatStyle is how one kind of conversation line is drawn.
type chatStyle struct {
	prefix  string
	fg      rgb
	oneLine bool // cut to one line instead of wrapping
}

func chatLineStyle(k chat.Kind) chatStyle {
	switch k {
	case chat.User:
		return chatStyle{prefix: "› ", fg: cGoldHi}
	case chat.Tool:
		return chatStyle{prefix: "⏺ ", fg: cMuted, oneLine: true}
	case chat.Note:
		return chatStyle{prefix: "· ", fg: cFaint}
	case chat.Assistant:
		return chatStyle{fg: cInk}
	}
	return chatStyle{fg: cSoft}
}

// chatContent is a session card's body: the end of the conversation, with the state at the bottom when
// it needs saying.
func (a *App) chatContent(t tile, iw, ih int) []string {
	s := t.sess
	var out []string
	add := func(fg rgb, text string) { out = append(out, paint(fg, cBg, " "+text)) }
	addWrapped := func(st chatStyle, text string, w int) {
		indent := strings.Repeat(" ", width(st.prefix))
		lines := wrap(text, w-width(st.prefix))
		if st.oneLine && len(lines) > 1 {
			lines = []string{fit(text, w-width(st.prefix))}
		}
		for i, l := range lines {
			if i == 0 {
				add(st.fg, st.prefix+l)
			} else {
				add(st.fg, indent+l)
			}
		}
	}
	ww := iw - 2

	if t.isItem() {
		add(cInbox, s.Detail)
		addWrapped(chatStyle{fg: cInk}, s.Title, ww)
		addWrapped(chatStyle{fg: cSoft}, s.LastPrompt, ww)
		if s.URL != "" {
			add(cFaint, s.URL)
		}
		return tailN(out, ih)
	}

	if lines := a.chatLines[s.ID]; len(lines) > 0 {
		for _, ln := range lines {
			addWrapped(chatLineStyle(ln.Kind), ln.Text, ww)
		}
	} else {
		addWrapped(chatStyle{fg: cSoft}, s.Title, ww)
		addWrapped(chatLineStyle(chat.User), s.LastPrompt, ww)
		if s.Heuristic {
			add(cFaint, "state estimated from activity")
		}
		add(cFaint, tilde(s.Cwd))
	}

	status := statusLine(t)
	if status == "" {
		return tailN(out, ih)
	}
	room := ih - 1
	out = tailN(out, room)
	for len(out) < room {
		out = append([]string{""}, out...)
	}
	return append(out, paint(look(s.State).color, cBg, " "+fit(status, ww)))
}

// statusLine is the state line at the bottom of a session card, or "" for idle sessions.
func statusLine(t tile) string {
	s := t.sess
	switch s.State {
	case model.Waiting:
		return "▲ waiting · " + firstOf(s.Detail, "input needed") + " · ⏎ to answer"
	case model.Stuck:
		return "✕ " + firstOf(s.Detail, "no activity")
	case model.Busy:
		return "● working · " + ago(since(t), time.Now())
	}
	return ""
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
	row("session", s.Name+"   "+strings.Join(meta, " · "), cInk)

	stateLine := look(s.State).label
	if s.Detail != "" {
		stateLine += " · " + s.Detail
	}
	stateLine += " · for " + ago(since(t), now)
	if s.Heuristic {
		stateLine += "  (estimated from activity)"
	}
	row("state", stateLine, look(s.State).color)
	row("dir", firstOf(tilde(s.Cwd), "—"), cSoft)
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
	if a.mode == insertMode {
		foot += paint(cGoldHi, cBg, "typing into the agent   ") + key("ctrl-q", "back to cockpit")
	} else {
		otherView := "grid"
		if a.view == gridView {
			otherView = "list"
		}
		foot += key("←↑↓→", "move") + key("⏎", "open") + key("n", "new") + key("v", otherView) +
			key("z", "zoom") + key("r", "reply") + key("!", "next needing me") + key("a", "filter") + key("q", "quit")
	}
	if msg := a.footerMessage(); msg != "" {
		if room := w - width(foot) - 1; room > 8 {
			foot += paint(cMuted, cBg, fit(msg, room))
		}
	}
	return pad(foot, w, cBg), cursor{}
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
