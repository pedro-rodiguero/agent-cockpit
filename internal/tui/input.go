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

// leaveLetter with ctrl takes the keyboard back from a pane (ctrl-q). Every other key goes to the agent.
const leaveLetter = "q"

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
		a.mode = navMode
	case k.code == keyPaste:
		a.flushTyped()
		if t, ok := a.typingTarget(); ok {
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
		a.selectIndex(a.cur() + a.lay.perPage)
	case actPageUp:
		a.selectIndex(a.cur() - a.lay.perPage)
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
		if t, ok := a.selected(); ok && t.pane == nil && t.sess != nil && t.sess.TTY != "" {
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
	}
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

// move goes one card in a direction: across a grid row, or by a whole row up and down.
func (a *App) move(dx, dy int) {
	if a.view == listView || a.zoom {
		a.selectIndex(a.cur() + dx + dy)
		return
	}
	if dx != 0 {
		a.selectIndex(a.cur() + dx)
	} else {
		a.selectIndex(a.cur() + dy*a.lay.cols)
	}
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

// activate does the obvious thing with the selected tile: type into its pane, go to its terminal tab,
// or open its link.
func (a *App) activate() {
	t, ok := a.selected()
	switch {
	case !ok:
	case t.livePane():
		a.view = gridView
		a.mode = insertMode
	case t.pane != nil:
		a.say("pane exited · x closes it")
	case t.sess != nil && t.sess.TTY != "":
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

func (a *App) focusTab(t tile) {
	tty, name := t.sess.TTY, t.name()
	a.goSafe(func() {
		if err := term.Focus(tty); err != nil {
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
	dir, _ := os.Getwd()
	if t, ok := a.selected(); ok {
		if t.pane != nil {
			dir = t.pane.Cwd
		} else if t.sess != nil && t.sess.Cwd != "" && !t.isItem() {
			dir = t.sess.Cwd
		}
	}
	a.openPrompt(&prompt{kind: promptNew, text: []rune(tilde(dir))})
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
	if !ok || t.pane == nil {
		a.say("x closes panes opened by cockpit")
		return
	}
	id, name := t.pane.ID, t.name()
	a.confirm("close "+name+"? the agent is stopped · y / n", func() {
		if a.send(mux.Msg{Op: mux.OpClose, ID: id}) {
			a.say("closed " + name)
		}
	})
}

// adopt moves a session running in a terminal tab into a pane: once confirmed, the tab's process is
// stopped and the same conversation is resumed in a pane (same dir, same profile, so memory, skills and
// MCP servers are the ones the session already had).
func (a *App) adopt() {
	t, ok := a.selected()
	if !ok || t.pane != nil || t.sess == nil {
		a.say("p moves a session from a Terminal tab into a pane")
		return
	}
	if a.muxUnavailable() {
		return
	}
	if err := resumable(*t.sess); err != nil {
		a.say(err.Error())
		return
	}
	id, pid := t.sess.ID, t.sess.PID
	question := "move " + t.name() + " into a pane? the tab's process ends and the conversation resumes here." +
		adoptWarning(t.sess.State) + " · y / n"
	a.confirm(question, func() { a.moveIntoPane(id, pid) })
}

// adoptWarning says what moving a session in state st loses.
func adoptWarning(st model.State) string {
	switch st {
	case model.Busy, model.Stuck:
		return " it is mid-turn: the turn is cut off."
	case model.Waiting:
		return " its pending question is dropped."
	}
	return ""
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
	a.goSafe(func() {
		if err := stopAgent(s, runsAgent, stopProcess); err != nil {
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
	promptNew     promptKind = iota // directory and agent for a new pane
	promptReply                     // a message for a Claude session
	promptConfirm                   // a yes/no question
)

type prompt struct {
	kind   promptKind
	label  string
	text   []rune
	agent  int    // promptNew: index into App.launchers
	target tile   // promptReply: the session to write to
	onYes  func() // promptConfirm: what "y" does
}

// insert adds typed or pasted text; control characters (a pasted newline) become spaces so the prompt
// stays on one line.
func (pr *prompt) insert(s string) {
	pr.text = append(pr.text, []rune(strings.Map(func(r rune) rune {
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
	switch {
	case k.code == keyEsc || k.isCtrl("c"):
		a.closePrompt()
	case pr.kind == promptConfirm:
		a.answer(k)
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
	case pr.kind == promptNew && (k.code == keyUp || k.code == keyShiftTab):
		pr.agent = (pr.agent + len(a.launchers) - 1) % len(a.launchers)
	case pr.kind == promptNew && k.code == keyDown:
		pr.agent = (pr.agent + 1) % len(a.launchers)
	case pr.kind == promptNew && k.code == keyTab:
		pr.text = []rune(completeDir(string(pr.text)))
	case k.code == keyPaste || (k.code == keyRune && !k.alt):
		pr.insert(k.text)
	}
}

// answer resolves a confirmation: "y" confirms, any other key declines. Pasted text does neither, so a
// paste that happens to contain "y" can never confirm.
func (a *App) answer(k key) {
	if k.code == keyPaste {
		return
	}
	pr := a.prompt
	a.closePrompt()
	if k.isRune("y") || k.isRune("Y") {
		pr.onYes()
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
