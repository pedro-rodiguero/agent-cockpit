package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

// A session card, top to bottom:
//
//	╭ ● api-auth ──────────────────── claude · s004 · 1 ╮   title: state, name, where it runs
//	│ ~/src/api ⎇ feat/limits          opus-5-5 · 212k │   context bar: directory, branch, model, tokens
//	│ Rate limiting for the login route                 │   subject: what the session is about
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

	// Rows a card must have inside before it spends one on the subject, and one on the pinned prompt.
	// Smaller cards keep those rows for the conversation.
	minRowsForSubject = 8
	minRowsForPrompt  = 10

	// maxToolRun is how many consecutive tool calls a card lists; a longer run shows its count and
	// only the latest ones, so tool noise does not push the agent's words out of the card.
	maxToolRun = 2

	// contextWarnPercent is how full a context window may get before its figure is highlighted.
	contextWarnPercent = 80
)

func (a *App) renderTile(t tile, cw, ch int, selected bool, num int) ([]string, cursor) {
	iw, ih := cw-cardBorderCols, ch-cardBorderCols
	insert := selected && a.mode == insertMode
	border := cardBorder(t, selected, insert)

	var body []string
	var cur cursor
	if t.pane != nil {
		body, cur = a.paneContent(t, iw, ih, insert)
	} else {
		body = a.sessionContent(t, iw, ih)
	}
	lines := make([]string, 0, ch)
	lines = append(lines, a.cardTop(t, cw, border, selected, num))
	side := paint(border, cBg, "│")
	for i := 0; i < ih; i++ {
		row := ""
		if i < len(body) {
			row = body[i]
		}
		lines = append(lines, side+pad(row, iw, cBg)+side)
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

// cardTop is the top border with the title: ╭ ▲ name ───── claude · s006 · 1 ╮
func (a *App) cardTop(t tile, cw int, border rgb, selected bool, num int) string {
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
	opening := paint(border, cBg, "╭ ") + paint(look(st).color, cBg, look(st).glyph+" ")
	const minFill, corner = 1, 1
	nameRoom := max(minNameWidth, cw-width(opening)-1-width(right)-minFill-corner)
	left := opening + paintBold(nameFg, cBg, fit(t.name(), min(nameRoom, width(t.name())))) + paint(cInk, cBg, " ")
	fill := cw - corner - width(left) - width(right)
	if fill < minFill {
		right = ""
		fill = cw - corner - width(left)
	}
	return pad(left+paint(border, cBg, strings.Repeat("─", max(0, fill)))+right+paint(border, cBg, "╮"), cw, cBg)
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
	bot := paint(border, cBg, "╰") + paint(hfg, cBg, hint) + paint(border, cBg, strings.Repeat("─", max(0, cw-cardBorderCols-width(hint)))+"╯")
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
		keys = []string{"⏎ continue here", "o go to tab"}
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

// sessionContent is the inside of a session or inbox card.
func (a *App) sessionContent(t tile, iw, ih int) []string {
	now := time.Now()
	if t.isItem() {
		return stack(ih, []string{itemBar(t, iw, now)}, conversation(t, nil, iw).rows, linkBar(t, iw))
	}
	conv := a.convs[t.sess.ID]
	head := []string{contextBar(t, conv.Info, iw)}
	if subject := t.sess.Title; subject != "" && ih >= minRowsForSubject {
		head = append(head, pad(paintBold(cSoft, cBg, " "+fit(subject, iw-2)), iw, cBg))
	}
	text := conversation(t, conv.Lines, iw)
	// Keep the last prompt in view when the answer to it has scrolled it out of the card.
	room := ih - len(head) - 1
	if text.lastPrompt != "" && ih >= minRowsForPrompt && len(text.rows)-text.lastPromptRow > room {
		head = append(head, promptRow(text.lastPrompt, iw))
	}
	return stack(ih, head, text.rows, stateBar(t, conv.Info, iw, now, a.isMovePending(t)))
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

// cardText is the rendered text of a card, and where the last prompt sits in it.
type cardText struct {
	rows          []string
	lastPrompt    string
	lastPromptRow int
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
			out = append(out, chat.Line{Kind: chat.Note, Text: fmt.Sprintf("%d tool calls", run-maxToolRun)})
			out = append(out, lines[j-maxToolRun:j]...)
			i = j
		}
	}
	return out
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
	var text cardText
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
		for _, ln := range foldToolRuns(lines) {
			if ln.Kind == chat.User {
				text.lastPrompt, text.lastPromptRow = ln.Text, len(text.rows)
			}
			add(chatLineStyle(ln.Kind), ln.Text)
		}
	default:
		// No readable transcript: the prompt the source reported is all there is.
		add(chatLineStyle(chat.User), s.LastPrompt)
	}
	return text
}
