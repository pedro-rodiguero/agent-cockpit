package tui

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/peer"
	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
)

// leaveLetter with ctrl takes the keyboard back from a pane (ctrl-q), and zoomLetter with ctrl zooms in
// or out of the pane being typed into (ctrl-z). Every other key goes to the agent, except Esc in a zoomed
// pane, which goes back to all the panes.
const (
	leaveLetter = "q"
	zoomLetter  = "z"
)

// action is what a key does in navigation mode.
type action int

const (
	actNone action = iota
	actQuit
	actEscape
	actToggleView
	actUp
	actDown
	actLeft
	actRight
	actFirst
	actLast
	actPageDown
	actPageUp
	actNext
	actPrev
	actNextAttention
	actZoom
	actToggleAttention
	actToggleLabels
	actActivate
	actFocusTab
	actNewPane
	actReply
	actClosePane
	actAdopt
	actHelp
	actNewTask
	actCompleteTask
	actEditTask
	actUndo
	actPlan
)

// binding is a key as navigation mode sees it.
type binding struct {
	code keyCode
	text string
}

var navBindings = map[binding]action{
	{keyRune, "q"}: actQuit, {keyCtrl, "c"}: actQuit,
	{keyEsc, ""}:   actEscape,
	{keyRune, "v"}: actToggleView,
	{keyUp, ""}:    actUp, {keyRune, "k"}: actUp,
	{keyDown, ""}: actDown, {keyRune, "j"}: actDown,
	{keyLeft, ""}: actLeft, {keyRune, "h"}: actLeft,
	{keyRight, ""}: actRight, {keyRune, "l"}: actRight,
	{keyHome, ""}: actFirst, {keyRune, "g"}: actFirst,
	{keyEnd, ""}: actLast, {keyRune, "G"}: actLast,
	{keyPgDown, ""}: actPageDown, {keyRune, "]"}: actPageDown,
	{keyPgUp, ""}: actPageUp, {keyRune, "["}: actPageUp,
	{keyTab, ""}:      actNext,
	{keyShiftTab, ""}: actPrev,
	{keyRune, "!"}:    actNextAttention,
	{keyRune, "z"}:    actZoom,
	{keyRune, "a"}:    actToggleAttention,
	{keyRune, "t"}:    actToggleLabels,
	{keyEnter, ""}:    actActivate, {keyRune, "i"}: actActivate,
	{keyRune, "o"}: actFocusTab,
	{keyRune, "n"}: actNewPane,
	{keyRune, "r"}: actReply,
	{keyRune, "x"}: actClosePane,
	{keyRune, "p"}: actAdopt,
	{keyRune, "?"}: actHelp,
	{keyRune, "+"}: actNewTask,
	{keyRune, " "}: actCompleteTask,
	{keyRune, "e"}: actEditTask,
	{keyRune, "u"}: actUndo,
	{keyRune, "m"}: actPlan,
}

// navAction maps a key to its action. Alt combinations and pastes do nothing: pasted text must never
// act as commands, let alone answer a confirmation.
func navAction(k key) action {
	if k.alt || k.code == keyPaste {
		return actNone
	}
	return navBindings[binding{k.code, k.text}]
}

// cardNumber returns the number 1–9 a digit key selects on the current page.
func cardNumber(k key) (int, bool) {
	if k.code != keyRune || k.alt || len(k.text) != 1 || k.text[0] < '1' || k.text[0] > '9' {
		return 0, false
	}
	return int(k.text[0] - '0'), true
}

// onKeys handles the keys of one read in order, each in the mode the previous one left.
func (a *App) onKeys(keys []key) {
	for _, k := range keys {
		if k.code == keyMouse {
			a.onMouse(k.mouse)
			continue
		}
		a.selecting = nil
		switch a.mode {
		case insertMode:
			a.insertKey(k)
		case promptMode:
			a.promptKey(k)
		default:
			a.navKey(k)
		}
		a.relayout()
		if a.quit {
			break
		}
	}
	a.flushTyped()
}

// insertKey passes k to the selected pane, batching typed keys so one read becomes one message.
func (a *App) insertKey(k key) {
	switch {
	case k.isCtrl(leaveLetter):
		a.flushTyped()
		a.mode, a.zoom = navMode, false
	case k.isCtrl(zoomLetter):
		a.zoom = !a.zoom
	case k.code == keyEsc && a.zoom:
		a.flushTyped()
		a.mode, a.zoom = navMode, false
	case k.code == keyPaste:
		a.flushTyped()
		if t, ok := a.typingTarget(); ok {
			delete(a.scrollBack, t.pane.ID)
			// The server adds bracketed-paste markers only if the program in the pane asked for them.
			a.send(mux.Msg{Op: mux.OpPaste, ID: t.pane.ID, Data: []byte(k.text)})
		}
	default:
		a.typed = append(a.typed, k.raw...)
	}
}

func (a *App) flushTyped() {
	if len(a.typed) == 0 {
		return
	}
	if t, ok := a.typingTarget(); ok {
		delete(a.scrollBack, t.pane.ID)
		a.send(mux.Msg{Op: mux.OpInput, ID: t.pane.ID, Data: a.typed})
	}
	a.typed = nil
}

// typingTarget is the pane insert mode types into. Without one, insert mode ends.
func (a *App) typingTarget() (tile, bool) {
	t, ok := a.selected()
	if !ok || !t.livePane() || a.mc == nil {
		a.mode = navMode
		return tile{}, false
	}
	return t, true
}

func (a *App) navKey(k key) {
	if a.help {
		a.help = false
		return
	}
	if a.doc != nil {
		a.docKey(k)
		return
	}
	if a.typesIntoZoomedPane(k) {
		a.mode = insertMode
		a.insertKey(k)
		return
	}
	if n, ok := cardNumber(k); ok {
		if i, ok := a.lay.onPage(n); ok {
			a.selectIndex(i)
		}
		return
	}
	switch navAction(k) {
	case actQuit:
		a.quit = true
	case actEscape:
		a.escape()
	case actToggleView:
		a.toggleView()
	case actUp:
		a.move(0, -1)
	case actDown:
		a.move(0, 1)
	case actLeft:
		a.move(-1, 0)
	case actRight:
		a.move(1, 0)
	case actFirst:
		a.selectIndex(0)
	case actLast:
		a.selectIndex(len(a.tiles) - 1)
	case actPageDown:
		a.page(1)
	case actPageUp:
		a.page(-1)
	case actNext:
		a.cycle(1)
	case actPrev:
		a.cycle(-1)
	case actNextAttention:
		a.selectNextNeedingAttention()
	case actZoom:
		if a.view == gridView {
			a.zoom = !a.zoom
		}
	case actToggleAttention:
		a.onlyAttn = !a.onlyAttn
		a.rebuild()
	case actToggleLabels:
		a.toggleLabels()
	case actActivate:
		a.activate()
	case actFocusTab:
		if t, ok := a.selected(); ok && t.hasTab() {
			a.focusTab(t)
		}
	case actNewPane:
		a.promptNewPane()
	case actReply:
		a.promptReply()
	case actClosePane:
		a.confirmClosePane()
	case actAdopt:
		a.adopt()
	case actHelp:
		a.help = true
	case actNewTask:
		a.promptNewTask()
	case actCompleteTask:
		a.completeTask()
	case actEditTask:
		a.promptEditTask()
	case actUndo:
		a.undoComplete()
	case actPlan:
		a.openPlan()
	}
}

// typesIntoZoomedPane reports whether k starts typing into the zoomed pane: in a zoom on a live pane
// every character and paste goes to the agent, except z, which leaves the zoom like Esc does.
func (a *App) typesIntoZoomedPane(k key) bool {
	if !a.zoom || a.view != gridView || k.isRune("z") || (k.code != keyRune && k.code != keyPaste) {
		return false
	}
	t, ok := a.selected()
	return ok && t.livePane() && a.mc != nil
}

// escape leaves zoom, or dismisses an alert.
func (a *App) escape() {
	if a.zoom {
		a.zoom = false
		return
	}
	a.flash = flash{}
}

func (a *App) toggleView() {
	if a.view == listView {
		a.view = gridView
	} else {
		a.view, a.zoom = listView, false
	}
	a.rebuild()
}

func (a *App) toggleLabels() {
	a.opts.LabelTabs = !a.opts.LabelTabs
	if a.opts.LabelTabs {
		a.updateLabels()
		a.say("tab titles: on")
	} else {
		a.clearLabels()
		a.say("tab titles: off")
	}
}

func (a *App) index(key string) int {
	for i, t := range a.tiles {
		if t.key == key {
			return i
		}
	}
	return -1
}

// cur is the index of the selected tile, or 0 when nothing is selected.
func (a *App) cur() int { return max(0, a.index(a.sel)) }

func (a *App) selected() (tile, bool) {
	if i := a.index(a.sel); i >= 0 {
		return a.tiles[i], true
	}
	return tile{}, false
}

// selectIndex selects tile i, clamped to the tiles there are.
func (a *App) selectIndex(i int) {
	if len(a.tiles) == 0 {
		return
	}
	a.sel = a.tiles[max(0, min(len(a.tiles)-1, i))].key
}

// cycle moves the selection by d, wrapping around.
func (a *App) cycle(d int) {
	if n := len(a.tiles); n > 0 {
		a.selectIndex(((a.cur()+d)%n + n) % n)
	}
}

// move goes one card in a direction: across a grid row, or by a whole row up and down. Right from the
// last column enters the rail, where up and down go through its rows and left returns to the grid.
func (a *App) move(dx, dy int) {
	l := a.lay
	if a.view == listView || a.zoom {
		a.selectIndex(a.cur() + dx + dy)
		return
	}
	i := a.cur()
	if l.inRail(i) {
		switch {
		case dy != 0:
			a.selectIndex(max(l.mainN, i+dy))
		case dx < 0 && l.mainN > 0:
			a.railSel = a.sel
			a.selectIndex(a.indexIn(a.gridSel, 0, l.mainN, l.first))
		}
		return
	}
	lastInRow := (i-l.first)%l.cols == l.cols-1 || i == l.mainN-1
	switch {
	case dx > 0 && lastInRow && l.rail.w > 0:
		a.gridSel = a.sel
		a.selectIndex(a.indexIn(a.railSel, l.mainN, len(a.tiles), l.mainN))
	case dx != 0:
		a.selectIndex(min(l.mainN-1, i+dx))
	default:
		a.selectIndex(min(l.mainN-1, i+dy*l.cols))
	}
}

// indexIn is the index of the tile with key in [from, to), or fallback when it is not there.
func (a *App) indexIn(key string, from, to, fallback int) int {
	if i := a.index(key); i >= from && i < to {
		return i
	}
	return fallback
}

// page moves the grid or list by d pages, staying among the cards.
func (a *App) page(d int) {
	l := a.lay
	if l.mainN == 0 {
		return
	}
	i := a.cur()
	if l.inRail(i) {
		i = l.first
	}
	a.selectIndex(max(0, min(l.mainN-1, i+d*l.perPage)))
}

func (a *App) selectNextNeedingAttention() {
	n := len(a.tiles)
	for d := 1; d <= n; d++ {
		if t := a.tiles[(a.cur()+d)%n]; t.state().NeedsAttention() {
			a.sel = t.key
			return
		}
	}
}

// activate does the obvious thing with the selected tile: type into its pane, continue a session from a
// terminal tab here (by moving it into a pane, after confirmation), go to the tab of a session that
// cannot be moved, or open an item's link.
func (a *App) activate() {
	t, ok := a.selected()
	switch {
	case !ok:
	case t.livePane():
		a.view = gridView
		a.mode = insertMode
	case t.pane != nil:
		a.reopen(t)
	case t.ownTask():
		a.promptEditTask()
	case t.canAdopt():
		a.adopt()
	case t.hasTab():
		a.focusTab(t)
	case t.sess != nil && t.sess.URL != "":
		url := t.sess.URL
		a.goSafe(func() {
			if err := term.Open(url); err != nil {
				a.post(event{kind: evFlash, msg: "open failed: " + err.Error()})
			}
		})
		a.say("opening " + url)
	}
}

// reopen replaces a pane whose program exited with a new one in the same directory: the same
// conversation when the pane ran a session cockpit can resume, a fresh start of the same agent otherwise.
func (a *App) reopen(t tile) {
	if a.muxUnavailable() {
		return
	}
	old := t.pane
	spec := mux.Msg{Op: mux.OpSpawn, Agent: old.Agent, Cwd: old.Cwd}
	what := "started " + old.Agent + " again in " + tilde(old.Cwd)
	if s, ok := a.paneSessions[old.ID]; ok {
		if resumed, err := resumeSpec(s); err == nil {
			spec, what = resumed, "resumed "+firstOf(s.Title, s.Name)
		}
	}
	spec.Cols, spec.Rows = a.newPaneSize()
	if a.send(mux.Msg{Op: mux.OpClose, ID: old.ID}) && a.send(spec) {
		a.say(what)
	}
}

func (a *App) focusTab(t tile) {
	tty, name, focus := t.sess.TTY, t.name(), a.focus
	a.goSafe(func() {
		if err := focus(tty); err != nil {
			a.post(event{kind: evFlash, msg: err.Error()})
			return
		}
		a.post(event{kind: evFlash, msg: "→ " + name})
	})
}

func (a *App) muxUnavailable() bool {
	if a.mc == nil {
		a.say("multiplexer unavailable: " + a.muxErr)
		return true
	}
	return false
}

func (a *App) promptNewPane() {
	if a.muxUnavailable() {
		return
	}
	text := newPaneDir(a.lastDirFile)
	a.openPrompt(&prompt{kind: promptNew, text: []rune(text), matches: dirMatches(text), matchedFor: text})
}

// newPaneDir is where the new pane prompt starts, as a terminal would: the folder above the one the last
// pane started in, so it and its siblings are listed. Before any pane, or when that folder is gone, it is
// the Desktop, or home when there is none. It ends in "/" so the folders inside are listed right away.
func newPaneDir(lastDirFile string) string {
	if b, err := os.ReadFile(lastDirFile); err == nil {
		if last := strings.TrimSpace(string(b)); filepath.IsAbs(last) {
			for dir := filepath.Dir(last); ; dir = filepath.Dir(dir) {
				if isDir(dir) {
					return strings.TrimSuffix(tilde(dir), "/") + "/"
				}
				if dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	if isDir(filepath.Join(home, "Desktop")) {
		return "~/Desktop/"
	}
	return "~/"
}

// rememberPaneDir records dir as where the last pane started, for the next new pane prompt.
func (a *App) rememberPaneDir(dir string) {
	if os.MkdirAll(filepath.Dir(a.lastDirFile), 0o700) == nil {
		_ = os.WriteFile(a.lastDirFile, []byte(dir+"\n"), 0o600)
	}
}

func (a *App) promptReply() {
	t, ok := a.selected()
	if !ok || !t.canReply() {
		a.say("reply works on Claude sessions · ⏎ goes to the session")
		return
	}
	if t.pane != nil {
		a.activate()
		return
	}
	a.openPrompt(&prompt{kind: promptReply, target: t, label: "reply to " + t.name()})
}

func (a *App) confirmClosePane() {
	t, ok := a.selected()
	if ok && t.ownTask() {
		a.confirmDeleteTask(t)
		return
	}
	if !ok || t.pane == nil {
		a.say("x closes panes opened by cockpit")
		return
	}
	id, name := t.pane.ID, t.name()
	a.confirm("close "+name+"? the agent is stopped · y·⏎ yes · n·esc no", func() {
		if a.send(mux.Msg{Op: mux.OpClose, ID: id}) {
			a.say("closed " + name)
		}
	})
}

// adopt moves a session running in a terminal tab into a pane: once confirmed, the tab's process is
// stopped and the same conversation is resumed in a pane (same dir, same profile, so memory, skills and
// MCP servers are the ones the session already had).
//
// The tab's process cannot be handed over alive (its terminal belongs to the terminal app), so moving
// means stopping it. A session in the middle of a turn, or waiting for an answer, is therefore not
// stopped right away: the move is scheduled and happens when the session is idle, so no work is lost.
func (a *App) adopt() {
	t, ok := a.selected()
	if !ok || t.pane != nil || t.sess == nil {
		a.say("only sessions in a terminal tab can be continued here")
		return
	}
	if a.muxUnavailable() {
		return
	}
	if err := resumable(*t.sess); err != nil {
		a.say(err.Error())
		return
	}
	id, pid, name := t.sess.ID, t.sess.PID, t.name()
	if _, waiting := a.pendingMoves[id]; waiting {
		a.confirm("cancel continuing "+name+" here? it stays in its tab · y·⏎ yes · n·esc no", func() {
			delete(a.pendingMoves, id)
			a.say(name + " stays in its tab")
		})
		a.offerTab(t)
		return
	}
	var question string
	switch t.sess.State {
	case model.Busy, model.Waiting:
		question = "continue " + name + " here? it is " + look(t.sess.State).label +
			": it moves when this turn ends, nothing is cut · y·⏎ yes · n·esc no"
	case model.Stuck:
		question = "continue " + name + " here? it looks stuck: its current turn is cut off · y·⏎ yes · n·esc no"
	default:
		question = "continue " + name + " here? its terminal tab stops and the conversation resumes in a pane · y·⏎ yes · n·esc no"
	}
	a.confirm(question, func() { a.requestMove(id, pid, t.sess.State, name) })
	a.offerTab(t)
}

// offerTab lets "o" answer the open question by going to the tile's terminal tab instead.
func (a *App) offerTab(t tile) {
	if !t.hasTab() {
		return
	}
	a.prompt.label += " · o goes to the tab instead"
	a.prompt.onO = func() { a.focusTab(t) }
}

// requestMove moves the session now if it is between turns or stuck, and otherwise schedules the move
// for when its turn ends.
func (a *App) requestMove(id string, pid int, st model.State, name string) {
	if !waitsForTurn(st) {
		a.moveIntoPane(id, pid)
		return
	}
	if a.pendingMoves == nil {
		a.pendingMoves = map[string]int{}
	}
	a.pendingMoves[id] = pid
	a.say(name + " moves here when this turn ends · ⏎ on it cancels")
}

// waitsForTurn reports whether a session in state st should finish its turn before it is moved.
func waitsForTurn(st model.State) bool { return st == model.Busy || st == model.Waiting }

// runPendingMoves moves every scheduled session whose turn has ended, and forgets the ones that went
// away or changed process meanwhile.
func (a *App) runPendingMoves() {
	for id, pid := range a.pendingMoves {
		s, err := adoptTarget(a.snap.Sessions, id, pid)
		switch {
		case err != nil:
			delete(a.pendingMoves, id)
			a.say("did not move " + firstOf(s.Name, id) + ": the session ended or restarted")
		case !waitsForTurn(s.State):
			delete(a.pendingMoves, id)
			a.moveIntoPane(id, pid)
		}
	}
}

// moveIntoPane stops the session that was confirmed and resumes it in a pane, provided it is still the
// same process.
func (a *App) moveIntoPane(id string, pid int) {
	s, err := adoptTarget(a.snap.Sessions, id, pid)
	if err != nil {
		a.say(err.Error())
		return
	}
	spec, err := resumeSpec(s)
	if err != nil {
		a.say(err.Error())
		return
	}
	spec.Cols, spec.Rows = a.newPaneSize()
	manual := resumeCommand(s)
	a.say("moving " + s.Name + "…")
	stop := a.stop
	a.goSafe(func() {
		if err := stop(s); err != nil {
			a.post(event{kind: evFlash, msg: "could not stop " + s.Name + ": " + err.Error()})
			return
		}
		a.post(event{kind: evAdoptStopped, msg: manual})
		if err := a.mc.Send(spec); err != nil {
			a.post(event{kind: evAlert, msg: stoppedNotResumed(err.Error(), manual)})
		}
	})
}

// ---------- prompt ----------

type promptKind int

const (
	promptNew      promptKind = iota // directory and agent for a new pane
	promptReply                      // a message for a Claude session
	promptConfirm                    // a yes/no question
	promptTask                       // a task of the to-do list, new or edited
	promptFeedback                   // feedback on the open document, for its session
)

type prompt struct {
	kind   promptKind
	label  string
	text   []rune
	agent  int    // promptNew: index into App.launchers
	target tile   // promptReply: the session to write to
	onYes  func() // promptConfirm: what "y" does
	onO    func() // promptConfirm: what "o" does, when the question offers an alternative
	taskID string // promptTask: the task being edited, "" for a new one
	group  string // promptTask: the group of a new task typed without #group

	// promptNew completes the directory as it is typed, the way a shell does: matches lists the
	// directories the text completes to, and while tab cycles through them (cycling), at is the one in
	// the field and before is the text as it was typed.
	matches    []string
	matchedFor string
	cycling    bool
	at         int
	before     string
}

// refreshMatches lists the directories the typed path completes to, unless that is already done.
func (pr *prompt) refreshMatches() {
	if t := string(pr.text); t != pr.matchedFor {
		pr.matches, pr.matchedFor = dirMatches(t), t
	}
}

// tab completes the path like a shell: first as far as every match agrees, then, when that adds
// nothing, by putting each match in the field in turn (step 1 forward, -1 back).
func (pr *prompt) tab(step int) {
	if !pr.cycling {
		if c := extendPath(string(pr.text), pr.matches); c != string(pr.text) {
			pr.text = []rune(c)
			pr.refreshMatches()
			return
		}
		if len(pr.matches) < 2 {
			return
		}
		pr.cycling, pr.before, pr.at = true, string(pr.text), -1
		if step < 0 {
			pr.at = 0
		}
	}
	n := len(pr.matches)
	pr.at = ((pr.at+step)%n + n) % n
	pr.text = []rune(pr.matches[pr.at])
}

// ghost is the rest of the path tab would add, shown after the cursor while typing. Tab also fixes the
// case of what was typed, which the ghost cannot show.
func (pr *prompt) ghost() string {
	if pr.cycling {
		return ""
	}
	t := string(pr.text)
	if c := extendPath(t, pr.matches); c != t && hasPrefixFold(c, t) {
		return string([]rune(c)[len(pr.text):])
	}
	return ""
}

// insert adds typed or pasted text; control characters (a pasted newline) become spaces so the prompt
// stays on one line.
// multiline reports whether the prompt takes several lines: messages for a session do.
func (pr *prompt) multiline() bool { return pr.kind == promptReply || pr.kind == promptFeedback }

func (pr *prompt) insert(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	pr.text = append(pr.text, []rune(strings.Map(func(r rune) rune {
		if r == '\n' && pr.multiline() {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))...)
}

// deleteWord removes the last path element or word.
func (pr *prompt) deleteWord() {
	s := strings.TrimRight(string(pr.text), "/ ")
	if i := strings.LastIndexAny(s, "/ "); i >= 0 {
		pr.text = []rune(s[:i+1])
	} else {
		pr.text = nil
	}
}

func (a *App) openPrompt(pr *prompt) { a.prompt, a.mode = pr, promptMode }

func (a *App) closePrompt() { a.prompt, a.mode = nil, navMode }

func (a *App) confirm(label string, onYes func()) {
	a.openPrompt(&prompt{kind: promptConfirm, label: label, onYes: onYes})
}

func (a *App) promptKey(k key) {
	pr := a.prompt
	if pr.kind == promptNew {
		a.pathKey(pr, k)
		return
	}
	switch {
	case pr.kind == promptConfirm:
		a.answer(k)
	case k.code == keyEsc || k.isCtrl("c"):
		a.closePrompt()
	case k.code == keyEnter && pr.multiline() && k.alt:
		pr.text = append(pr.text, '\n')
	case k.code == keyEnter && pr.multiline() && len(pr.text) > 0 && pr.text[len(pr.text)-1] == '\\':
		pr.text[len(pr.text)-1] = '\n' // as in Claude Code: a backslash before enter starts a new line
	case k.code == keyEnter:
		a.submit()
	case k.code == keyBackspace:
		if len(pr.text) > 0 {
			pr.text = pr.text[:len(pr.text)-1]
		}
	case k.isCtrl("u"):
		pr.text = nil
	case k.isCtrl("w"):
		pr.deleteWord()
	case k.code == keyPaste || (k.code == keyRune && !k.alt):
		pr.insert(k.text)
	}
}

// pathKey handles a key in the new-pane prompt: the agent picker, and the directory, completed as typed.
// Esc while tab cycles goes back to the text typed; any key but tab and shift-tab keeps the match in the
// field and carries on from it.
func (a *App) pathKey(pr *prompt, k key) {
	if !pr.cycling {
		pr.refreshMatches()
	}
	if pr.cycling && k.code != keyTab && k.code != keyShiftTab {
		pr.cycling = false
		if k.code == keyEsc {
			pr.text = []rune(pr.before)
			pr.refreshMatches()
			return
		}
	}
	switch {
	case k.code == keyEsc || k.isCtrl("c"):
		a.closePrompt()
		return
	case k.code == keyEnter:
		a.submit()
		return
	case k.code == keyTab:
		pr.tab(1)
		return
	case k.code == keyShiftTab && pr.cycling:
		pr.tab(-1)
		return
	case k.code == keyUp || k.code == keyLeft || k.code == keyShiftTab:
		pr.agent = (pr.agent + len(a.launchers) - 1) % len(a.launchers)
	case k.code == keyDown || k.code == keyRight:
		pr.agent = (pr.agent + 1) % len(a.launchers)
	case k.code == keyBackspace:
		if len(pr.text) > 0 {
			pr.text = pr.text[:len(pr.text)-1]
		}
	case k.isCtrl("u"):
		pr.text = nil
	case k.isCtrl("w"):
		pr.deleteWord()
	case k.code == keyPaste || (k.code == keyRune && !k.alt):
		pr.insert(k.text)
	}
	pr.refreshMatches()
}

// answer resolves a confirmation: "y" or Enter confirms, any other key (Esc included) declines. Pasted
// text does neither, so a paste that happens to contain "y" or a newline can never confirm.
func (a *App) answer(k key) {
	if k.code == keyPaste {
		return
	}
	pr := a.prompt
	a.closePrompt()
	switch {
	case k.isRune("y") || k.isRune("Y") || k.code == keyEnter:
		pr.onYes()
	case k.isRune("o") && pr.onO != nil:
		pr.onO()
	}
}

func (a *App) submit() {
	pr := a.prompt
	text := strings.TrimSpace(string(pr.text))
	switch pr.kind {
	case promptNew:
		a.startPane(a.launchers[pr.agent], text)
	case promptReply:
		a.closePrompt()
		if text != "" {
			a.sendReply(pr.target, text)
		}
	case promptFeedback:
		a.closePrompt()
		if text != "" && a.doc != nil {
			a.sendReply(pr.target, feedbackMessage(a.doc.path, text))
		}
	case promptTask:
		a.closePrompt()
		if text != "" {
			a.saveTask(pr.taskID, pr.group, text)
		}
	}
}

// startPane spawns l in the directory typed. An invalid one keeps the prompt open to be corrected.
func (a *App) startPane(l launcher, typed string) {
	dir, err := filepath.Abs(untilde(typed))
	if err != nil || !isDir(dir) {
		a.say("not a directory: " + typed)
		return
	}
	a.closePrompt()
	cols, rows := a.newPaneSize()
	if a.send(mux.Msg{Op: mux.OpSpawn, Agent: l.agent, Env: profileEnv(l.profile), Cwd: dir, Cols: cols, Rows: rows}) {
		a.rememberPaneDir(dir)
		a.say("starting " + l.label + " in " + tilde(dir) + "…")
	}
}

func (a *App) sendReply(t tile, text string) {
	s, name := t.sess, t.name()
	a.goSafe(func() {
		if err := peer.Send(s.SessionsDir, s.PID, s.NativeID, s.Socket, text); err != nil {
			a.post(event{kind: evFlash, msg: "reply failed: " + err.Error()})
			return
		}
		a.post(event{kind: evFlash, msg: "sent to " + name})
	})
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
