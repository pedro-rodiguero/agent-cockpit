package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// A session card, top to bottom:
//
//	╭ ● Rate limiting for login ────── claude · s004 · 1 ╮   title: state, subject, where it runs
//	│ ~/src/api ⎇ feat/limits          opus-5-5 · 212k │   context bar: directory, branch, model, tokens
//	│ › add rate limiting to the login route            │   the last prompt, kept in view
//	│ ⏺ Edit ~/src/api/limits.ts                        │   the end of the conversation
//	│ Done. Want me to wire it into the gateway?        │
//	│ ● working 2m · Bash npm test         plan · high │   state bar: what it is doing right now
//	╰ ⏎ continue here · o go to tab · r reply ──────────╯   keys, on the selected card
//
// A pane card shows the agent's own terminal between the borders instead; the agent draws its own status.
const (
	cardBorderCols = 2  // left and right border of a card
	minNameWidth   = 4  // a card title keeps at least this much of the name
	maxCardNumber  = 9  // cards beyond this have no digit key
	minDirWidth    = 12 // the context bar keeps at least this much of the directory
	branchGlyph    = "⎇ "

	// Rows a card must have inside before it spends one on the pinned prompt. Smaller cards keep that
	// row for the conversation.
	minRowsForPrompt = 8

	// maxToolRun is how many consecutive tool calls a card lists; a longer run shows its count and
	// only the latest ones, so tool noise does not push the agent's words out of the card.
	maxToolRun = 2

	// contextWarnPercent is how full a context window may get before its figure is highlighted.
	contextWarnPercent = 80
)

func (a *App) renderTile(t tile, cw, ch int, selected bool, num int, drop dropRole) ([]string, cursor) {
	iw, ih := cw-cardBorderCols, ch-cardBorderCols
	insert := selected && a.mode == insertMode
	border := cardBorder(t, selected, insert)
	// A card held by the mouse is drawn with a faint dashed border, as if lifted off the grid, and the
	// card it would land on with a bright one that says so.
	frame := cardFrame{border: border, rule: "─", side: "│", note: insertNote(insert, a.zoom)}
	switch drop {
	case dropHeld:
		frame = cardFrame{border: cFaint, rule: "╌", side: "┆", note: " moving · drop it on another card "}
	case dropTarget:
		frame = cardFrame{border: cGoldHi, rule: "━", side: "┃", note: " drop here to swap places "}
	}

	var body []string
	var cur cursor
	if t.pane != nil {
		body, cur = a.paneContent(t, iw, ih, insert)
	} else {
		body = a.sessionContent(t, iw, ih)
	}
	lines := make([]string, 0, ch)
	lines = append(lines, a.cardTop(t, cw, frame, selected, num))
	side := paint(frame.border, cBg, frame.side)
	if drop != dropNone {
		body, cur = a.dropBody(body, iw, ih, drop), cursor{}
	}
	for i := 0; i < ih; i++ {
		row := ""
		if i < len(body) {
			row = body[i]
		}
		lines = append(lines, side+pad(row, iw, cBg)+side)
	}
	lines = append(lines, cardBottom(cw, frame))
	if cur.show {
		cur.x++
		cur.y++
	}
	return lines, cur
}

// dropBody is a card's content during a move with the mouse: washed out in the card that is held, and
// lit in the card it would land on, with a sign in the middle saying what a release does.
func (a *App) dropBody(body []string, iw, ih int, drop dropRole) []string {
	fg, bg, sign := cFaint, cBg, "✥ moving"
	if drop == dropTarget {
		fg, bg, sign = cSoft, cPanelHi, "⇄ swap places"
		if held, _ := a.moving(); held >= 0 {
			sign = "⇄ swap with " + a.tiles[held].name()
		}
	}
	out := make([]string, ih)
	for i := range out {
		row := ""
		if i < len(body) {
			row = ansi.Strip(body[i])
		}
		out[i] = paint(fg, bg, row) + paint(fg, bg, strings.Repeat(" ", max(0, iw-width(row))))
	}
	sign = ansi.Truncate(sign, max(0, iw-4), "…")
	if ih == 0 || width(sign) == 0 {
		return out
	}
	// The sign sits on a band of its own, a blank row above and below it when the card is tall enough.
	mid, left := ih/2, (iw-width(sign)-2)/2
	band := func(text string) string {
		return paintBold(cBg, cGoldHi, " "+text+strings.Repeat(" ", width(sign)-width(text))+" ")
	}
	for y, text := range map[int]string{mid - 1: "", mid: sign, mid + 1: ""} {
		if y < 0 || y >= ih || (y != mid && ih < 5) {
			continue
		}
		row := ""
		if y < len(body) {
			row = ansi.Strip(body[y])
		}
		row += strings.Repeat(" ", max(0, iw-width(row)))
		out[y] = paint(fg, bg, ansi.Cut(row, 0, left)) + band(text) + paint(fg, bg, ansi.Cut(row, left+width(sign)+2, iw))
	}
	return out
}

// cardFrame is how a card's border is drawn: its color, the characters of its lines, and the note on its
// bottom edge.
type cardFrame struct {
	border     rgb
	rule, side string
	note       string
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

// cardTop is the top border with the title: ╭ ▲ name ───── claude · s006 · 1 ╮
func (a *App) cardTop(t tile, cw int, frame cardFrame, selected bool, num int) string {
	border := frame.border
	st := t.state()
	nameFg := cSoft
	switch {
	case selected:
		nameFg = cGoldHi
	case st.NeedsAttention():
		nameFg = cInk
	}
	where := []string{agentLabel(t)}
	if t.sess != nil && t.sess.TTY != "" && t.pane == nil {
		where = append(where, strings.TrimPrefix(t.sess.TTY, "tty"))
	}
	if num <= maxCardNumber && !a.zoom {
		where = append(where, fmt.Sprint(num))
	}
	right := paint(cMuted, cBg, " "+strings.Join(where, " · ")+" ")
	opening := paint(border, cBg, frame.corners()[0]+" ") + paint(look(st).color, cBg, look(st).glyph+" ")
	const minFill, corner = 1, 1
	nameRoom := max(minNameWidth, cw-width(opening)-1-width(right)-minFill-corner)
	left := opening + paintBold(nameFg, cBg, fit(t.name(), min(nameRoom, width(t.name())))) + paint(cInk, cBg, " ")
	fill := cw - corner - width(left) - width(right)
	if fill < minFill {
		right = ""
		fill = cw - corner - width(left)
	}
	return pad(left+paint(border, cBg, strings.Repeat(frame.rule, max(0, fill)))+right+paint(border, cBg, frame.corners()[1]), cw, cBg)
}

// isMovePending reports whether the tile's session is scheduled to move into a pane.
func (a *App) isMovePending(t tile) bool {
	if t.sess == nil {
		return false
	}
	_, ok := a.pendingMoves[t.sess.ID]
	return ok
}

// agentLabel names what runs in the card: the agent, its Claude profile, and whether it is a pane.
func agentLabel(t tile) string {
	label := t.agent()
	if t.sess != nil && t.sess.Profile != "" {
		label += " " + t.sess.Profile
	}
	if t.pane != nil {
		label += " ◧"
	}
	return label
}

// corners are the frame's four corners, top left, top right, bottom left, bottom right: heavy ones for
// heavy lines.
func (f cardFrame) corners() [4]string {
	if f.rule == "━" {
		return [4]string{"┏", "┓", "┗", "┛"}
	}
	return [4]string{"╭", "╮", "╰", "╯"}
}

// insertNote says how to leave a pane while the person types into it; the keys for a selected card are
// in the footer.
func insertNote(insert, zoom bool) string {
	switch {
	case insert && zoom:
		return " typing into the agent · esc all panes · ctrl-q leaves "
	case insert:
		return " typing into the agent · ctrl-z zoom · ctrl-q leaves "
	}
	return ""
}

// cardBottom closes the card, with the frame's note when there is room for it.
func cardBottom(cw int, frame cardFrame) string {
	hint := frame.note
	if width(hint) > cw-4 {
		hint = ""
	}
	c := frame.corners()
	bot := paint(frame.border, cBg, c[2]) + paint(cGoldHi, cBg, hint) + paint(frame.border, cBg, strings.Repeat(frame.rule, max(0, cw-cardBorderCols-width(hint)))+c[3])
	return pad(bot, cw, cBg)
}

// keyHint is a key and what it does.
type keyHint struct{ key, does string }

// tileKeys lists the keys that act on t, the one that does the obvious thing first.
func tileKeys(t tile) []keyHint {
	switch {
	case t.pane != nil && t.pane.Exited:
		return []keyHint{{"⏎", "reopen"}, {"x", "close"}, {"z", "zoom"}}
	case t.pane != nil:
		return []keyHint{{"⏎", "type into it"}, {"z", "zoom"}, {"s", "aside"}, {"x", "close"}}
	case t.ownTask():
		return []keyHint{{"space", "done"}, {"⏎", "edit"}, {"x", "delete"}, {"+", "new task"}}
	case t.isItem():
		return []keyHint{{"⏎", "open"}}
	}
	var keys []keyHint
	switch {
	case t.canAdopt():
		keys = append(keys, keyHint{"⏎", "continue here"})
		if t.hasTab() {
			keys = append(keys, keyHint{"o", "go to tab"})
		}
	case t.hasTab():
		keys = append(keys, keyHint{"⏎", "go to tab"})
	}
	if t.canReply() {
		keys = append(keys, keyHint{"r", "reply"})
	}
	return append(keys, keyHint{"z", "zoom"})
}

func (a *App) paneContent(t tile, iw, ih int, insert bool) ([]string, cursor) {
	var cur cursor
	pn := t.pane
	scr := a.mc.Screen(pn.ID)
	if scr == nil {
		return []string{paint(cMuted, cBg, " starting "+pn.Agent+"…")}, cur
	}
	if back := a.scrollBack[pn.ID]; back > 0 {
		if h := a.mc.History(pn.ID); h != nil && h.Back > 0 {
			return historyContent(h, iw, ih), cur
		}
	}
	body := make([]string, 0, ih)
	for i := 0; i < ih && i < len(scr.Lines); i++ {
		body = append(body, seeThrough(scr.Lines[i]))
	}
	if pn.Exited {
		msg := fmt.Sprintf(" exited · code %d · ⏎ reopens it · x closes it ", pn.ExitCode)
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

// paneBg is the background the pane server paints a cell the program left on its default background.
var paneBg = func() rgb {
	r, g, b, _ := mux.DefaultBg.RGBA()
	return rgb{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)}
}()

var paneBgSeq = fmt.Sprintf(";48;2;%d;%d;%dm", paneBg.r, paneBg.g, paneBg.b)

// seeThrough leaves a pane line's default background to the terminal when the page's is, as it is in a
// translucent window: a terminal paints any explicit color opaque, so the panes alone would hide the
// desktop. Only where that background is the page's own, in the dark theme, so nothing changes color.
func seeThrough(line string) string {
	if !terminalBg || cBg != paneBg {
		return line
	}
	return strings.ReplaceAll(line, paneBgSeq, "m")
}

// historyContent is a pane scrolled up into its history, with a bar saying how far and how to return.
func historyContent(h *mux.Screen, iw, ih int) []string {
	body := make([]string, 0, ih)
	for i := 0; i < ih && i < len(h.Lines); i++ {
		body = append(body, seeThrough(h.Lines[i]))
	}
	for len(body) < ih {
		body = append(body, "")
	}
	msg := fmt.Sprintf(" ↑ %d of %d lines up · scroll down or type to return ", h.Back, h.MaxBack)
	body[ih-1] = pad(paint(cGold, cPanel, msg), iw, cPanel)
	return body
}

// sessionContent is the inside of a session or inbox card.
func (a *App) sessionContent(t tile, iw, ih int) []string {
	now := time.Now()
	if t.isItem() {
		return stack(ih, []string{itemBar(t, iw, now)}, conversation(t, nil, iw).rows, linkBar(t, iw))
	}
	conv := a.convs[t.sess.ID]
	head := []string{contextBar(t, conv.Info, iw)}
	text := conversation(t, conv.Lines, iw)
	// Keep the last prompt in view when the answer to it has scrolled it out of the card.
	room := ih - len(head) - 1
	if text.lastPrompt != "" && ih >= minRowsForPrompt && len(text.rows)-text.lastPromptRow > room {
		head = append(head, promptRow(text.lastPrompt, iw))
	}
	body := text.rows
	if t.state() != model.Busy && t.state() != model.Waiting {
		body = excerpt(text, ih-len(head)-1, iw)
	}
	return stack(ih, head, body, stateBar(t, conv.Info, iw, now, a.isMovePending(t)))
}

// excerpt fits the end of a finished turn into room rows. When the agent's last reply is longer than
// the card, the card shows it from its beginning rather than from the middle of a sentence, cut where
// the room ends, and keeps the closing question, if the reply ends with one, below the cut. A working
// session shows the end of its conversation instead, where things are happening.
func excerpt(text cardText, room, iw int) []string {
	rows := text.rows
	start := text.lastReplyRow
	if room <= 0 || start < 0 || len(rows)-start <= room {
		return rows
	}
	var question []string
	if text.questionRow > start {
		question = rows[text.questionRow:]
	}
	keep := room - 1 - len(question)
	if keep < 1 {
		return rows
	}
	cut := pad(paint(cFaint, cBg, " ⋯"), iw, cBg)
	out := append(append([]string{}, rows[start:start+keep]...), cut)
	return append(out, question...)
}

// stack fills h rows with head at the top, foot at the bottom, and the end of body between them.
func stack(h int, head, body []string, foot string) []string {
	if h <= len(head) {
		return head[:max(0, h)]
	}
	out := append(make([]string, 0, h), head...)
	out = append(out, tailN(body, h-len(head)-1)...)
	for len(out) < h-1 {
		out = append(out, "")
	}
	return append(out, foot)
}

// bar lays out one full-width row on the panel background with left text and right-aligned fields.
// rights are alternatives for the right side, most complete first: the first one that fits next to the
// whole left text is used. When none fits the left text takes the row, cut if it has to be.
func bar(left string, rights []string, w int) string {
	right := ""
	for _, r := range rights {
		if width(left)+width(r) <= w {
			right = r
			break
		}
	}
	if width(left) > w {
		left = ansi.Truncate(left, max(0, w-1), "") + paint(cFaint, cPanel, "…")
	}
	gap := max(0, w-width(left)-width(right))
	return left + paint(cInk, cPanel, strings.Repeat(" ", gap)) + right
}

// contextBar says where a session works and with what: directory and branch on the left; model and
// context size on the right. As the row narrows, the directory is shortened from its start (its end is
// what tells sessions apart) down to minDirWidth; then the model goes, then the branch, then the size.
func contextBar(t tile, info chat.Info, w int) string {
	field := func(fg rgb, text string) string { return paint(fg, cPanel, text) }
	ctx, ctxFg := contextSize(info)
	var parts []string
	if info.Model != "" {
		parts = append(parts, field(cMuted, info.Model))
	}
	if ctx != "" {
		parts = append(parts, field(ctxFg, ctx))
	}
	dir := tilde(t.sess.Cwd)
	branch := ""
	if info.Branch != "" {
		branch = "  " + branchGlyph + info.Branch
	}
	// Drop fields, least important first, until the shortened directory has room.
	const margins = 2
	right := func() string {
		if len(parts) == 0 {
			return ""
		}
		return " " + strings.Join(parts, field(cFaint, " · ")) + field(cInk, " ")
	}
	need := func() int { return margins + min(width(dir), minDirWidth) + width(branch) + width(right()) }
	if need() > w && len(parts) == 2 {
		parts = parts[1:]
	}
	if need() > w {
		branch = ""
	}
	if need() > w {
		parts = nil
	}
	dirRoom := max(1, w-margins-width(branch)-width(right()))
	left := field(cSoft, " "+strings.TrimRight(fitLeft(dir, min(dirRoom, width(dir))), " ")) + field(cMuted, branch)
	return bar(left, []string{right()}, w)
}

// contextSize formats how much of the context window is in use, highlighted when it is nearly full.
// The window size is only known for agents whose log states it.
func contextSize(info chat.Info) (string, rgb) {
	if info.ContextTokens == 0 {
		return "", cMuted
	}
	if info.ContextWindow == 0 {
		return tokens(info.ContextTokens) + " ctx", cMuted
	}
	fg := cMuted
	if info.ContextTokens*100 >= info.ContextWindow*contextWarnPercent {
		fg = cWaiting
	}
	return fmt.Sprintf("%s/%s ctx", tokens(info.ContextTokens), tokens(info.ContextWindow)), fg
}

// tokens abbreviates a token count: 950, 37k, 1.2M.
func tokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// stateBar says what a session is doing right now: its state, for how long, and the running tool call;
// permission mode and effort sit on the right.
func stateBar(t tile, info chat.Info, w int, now time.Time, movePending bool) string {
	s := t.sess
	l := look(s.State)
	left := paintBold(l.color, cPanel, " "+l.glyph+" "+l.label) + paint(l.color, cPanel, " "+ago(since(t), now))
	if movePending {
		left += paint(cFaint, cPanel, " · ") + paint(cGoldHi, cPanel, "moves here when this turn ends")
	} else if detail := stateDetail(*s, info); detail != "" {
		left += paint(cFaint, cPanel, " · ") + paint(cSoft, cPanel, detail)
	}
	var fields []string
	if info.Mode != "" {
		fields = append(fields, info.Mode)
	}
	if info.Effort != "" {
		fields = append(fields, info.Effort)
	}
	var rights []string
	for n := len(fields); n > 0; n-- {
		rights = append(rights, paint(cFaint, cPanel, " "+strings.Join(fields[:n], " · ")+" "))
	}
	return bar(left, rights, w)
}

// stateDetail is the reason behind a state: what the agent waits for, why it looks stuck, or the tool
// call it is in the middle of.
func stateDetail(s model.Session, info chat.Info) string {
	switch s.State {
	case model.Waiting:
		return firstOf(s.Detail, "input needed")
	case model.Stuck:
		return firstOf(s.Detail, "no activity")
	case model.Busy:
		switch {
		case info.Running > 1:
			return fmt.Sprintf("%s  +%d more", info.Activity, info.Running-1)
		case info.Activity != "":
			return info.Activity
		case s.Heuristic:
			return "estimated from activity"
		}
		return s.Detail
	}
	if s.Heuristic {
		return "estimated from activity"
	}
	return s.Detail
}

// itemBar heads an inbox card: what kind of item it is and how long it has waited.
func itemBar(t tile, w int, now time.Time) string {
	s := t.sess
	left := paintBold(cInbox, cPanel, " "+firstOf(s.Detail, "item"))
	right := paint(cMuted, cPanel, " "+ago(since(t), now)+" ")
	return bar(left, []string{right}, w)
}

// linkBar is the bottom row of an inbox card: where ⏎ leads.
func linkBar(t tile, w int) string {
	return bar(paint(cFaint, cPanel, " "+t.sess.URL), nil, w)
}

// chatStyle is how one kind of conversation line is drawn.
type chatStyle struct {
	prefix  string
	fg      rgb
	bg      rgb
	oneLine bool // cut to one line instead of wrapping
	gap     bool // blank line before it, when it is not the first line
}

// cardText is the rendered text of a card, and where its landmarks sit in it. A row index is -1 when
// the text has no such part.
type cardText struct {
	rows          []string
	lastPrompt    string
	lastPromptRow int
	lastReplyRow  int // first row of the agent's last reply: the prose after the last prompt or tool call
	questionRow   int // first row of the question the conversation ends on
}

// promptRow is a prompt cut to one row, as pinned below a card's header.
func promptRow(text string, iw int) string {
	st := chatLineStyle(chat.User)
	return pad(paint(st.fg, st.bg, " "+st.prefix+fit(text, iw-2-width(st.prefix))), iw, st.bg)
}

// foldToolRuns replaces each run of more than maxToolRun consecutive tool calls with a count and the
// latest maxToolRun of them.
func foldToolRuns(lines []chat.Line) []chat.Line {
	out := make([]chat.Line, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i
		for j < len(lines) && lines[j].Kind == chat.Tool {
			j++
		}
		switch run := j - i; {
		case run == 0:
			out = append(out, lines[i])
			i++
		case run <= maxToolRun:
			out = append(out, lines[i:j]...)
			i = j
		default:
			out = append(out, chat.Line{Kind: chat.Note, Text: toolCalls(run - maxToolRun)})
			out = append(out, lines[j-maxToolRun:j]...)
			i = j
		}
	}
	return out
}

// toolCalls says how many tool calls were left out: "1 tool call", "3 tool calls".
func toolCalls(n int) string {
	if n == 1 {
		return "1 tool call"
	}
	return fmt.Sprintf("%d tool calls", n)
}

func chatLineStyle(k chat.Kind) chatStyle {
	switch k {
	case chat.User:
		return chatStyle{prefix: "› ", fg: cGoldHi, bg: cPanel, gap: true}
	case chat.Tool:
		return chatStyle{prefix: "⏺ ", fg: cMuted, bg: cBg, oneLine: true}
	case chat.Note:
		return chatStyle{prefix: "· ", fg: cFaint, bg: cBg}
	case chat.Assistant:
		return chatStyle{fg: cInk, bg: cBg}
	}
	return chatStyle{fg: cSoft, bg: cBg}
}

// conversation renders the text of a session or inbox card as rows iw cells wide.
func conversation(t tile, lines []chat.Line, iw int) cardText {
	s := t.sess
	text := cardText{lastPromptRow: -1, lastReplyRow: -1, questionRow: -1}
	textW := iw - 2 // one cell of margin on each side
	add := func(st chatStyle, body string) {
		if body == "" {
			return
		}
		if st.gap && len(text.rows) > 0 {
			text.rows = append(text.rows, "")
		}
		rows := wrap(body, textW-width(st.prefix))
		if st.oneLine && len(rows) > 1 {
			rows = []string{fit(body, textW-width(st.prefix))}
		}
		indent := strings.Repeat(" ", width(st.prefix))
		for i, row := range rows {
			lead := indent
			if i == 0 {
				lead = st.prefix
			}
			text.rows = append(text.rows, pad(paint(st.fg, st.bg, " "+lead+row), iw, st.bg))
		}
	}
	switch {
	case t.isItem():
		add(chatStyle{fg: cInk, bg: cBg}, s.Title)
		add(chatStyle{fg: cSoft, bg: cBg, gap: true}, s.LastPrompt)
	case len(lines) > 0:
		folded := foldToolRuns(lines)
		prev := chat.Note
		for i, ln := range folded {
			st := chatLineStyle(ln.Kind)
			switch {
			case ln.Kind == chat.User:
				text.lastPrompt, text.lastPromptRow = ln.Text, len(text.rows)
				text.lastReplyRow = -1
			case ln.Kind == chat.Assistant && prev != chat.Assistant:
				text.lastReplyRow = len(text.rows)
			case ln.Kind == chat.Tool:
				text.lastReplyRow = -1
			}
			if i == len(folded)-1 && t.state() == model.Asked && ln.Kind == chat.Assistant {
				text.questionRow = len(text.rows)
				st.fg = cAsked
			}
			add(st, ln.Text)
			prev = ln.Kind
		}
	default:
		// No readable transcript: the prompt the source reported is all there is.
		add(chatLineStyle(chat.User), s.LastPrompt)
	}
	return text
}
