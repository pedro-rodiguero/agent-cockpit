package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// docView shows a Markdown file over the body, read again whenever it changes on disk: a session's plan
// is rewritten while the agent works on it. Feedback typed in the viewer goes to the session.
type docView struct {
	path   string
	title  string
	target tile // the session the document belongs to, and feedback goes to

	scroll int // first line shown

	// The file as last read and laid out: lines for width w, from a file of this size and time.
	lines    []string
	heads    []int
	w        int
	size     int64
	mtime    time.Time
	updated  time.Time // when the file last changed while open
	missing  bool
	bodyRows int // rows the last frame had for the document, for paging
}

// planPath is where a Claude Code session keeps its plan: <config>/plans/<slug>.md, next to the
// sessions directory of its profile.
func planPath(sessionsDir, slug string) string {
	dir := filepath.Dir(sessionsDir)
	if sessionsDir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "plans", slug+".md")
}

// openPlan shows the selected session's plan.
func (a *App) openPlan() {
	t, ok := a.selected()
	if !ok || t.sess == nil || t.sess.Agent != "claude" {
		a.say("m shows the plan of a Claude Code session")
		return
	}
	slug := a.convs[t.sess.ID].Info.Plan
	if slug == "" {
		a.say("no plan yet: " + t.name() + " has not been in plan mode")
		return
	}
	path := planPath(t.sess.SessionsDir, slug)
	if !isFile(path) {
		a.say("plan not found: " + tilde(path))
		return
	}
	a.doc = &docView{path: path, title: "plan · " + t.name(), target: t}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// load reads and lays out the file again when it changed since the last frame, or the width did.
func (d *docView) load(w int) {
	fi, err := os.Stat(d.path)
	if err != nil {
		d.missing = true
		return
	}
	d.missing = false
	if d.lines != nil && w == d.w && fi.Size() == d.size && fi.ModTime().Equal(d.mtime) {
		return
	}
	b, err := os.ReadFile(d.path)
	if err != nil {
		d.missing = true
		return
	}
	if d.lines != nil && (fi.Size() != d.size || !fi.ModTime().Equal(d.mtime)) {
		d.updated = time.Now()
	}
	d.lines, d.heads = renderMarkdown(string(b), w)
	d.w, d.size, d.mtime = w, fi.Size(), fi.ModTime()
}

// docTextWidth is the width of the text column: the body less its margins, and no wider than reads well.
const docMaxText = 100

func docTextWidth(w int) int { return max(8, min(docMaxText, w-4)) }

// docBody draws the viewer in a w×h body: a title row, then the document.
func (a *App) docBody(w, h int) []string {
	d := a.doc
	tw := docTextWidth(w)
	d.load(tw)
	rows := max(1, h-2)
	d.bodyRows = rows
	d.scroll = max(0, min(d.scroll, len(d.lines)-rows))

	// The path goes last: it is the long part, and the one a narrow window can lose.
	var info string
	if !d.updated.IsZero() {
		info += "updated " + d.updated.Format("15:04:05") + "  ·  "
	}
	if n := len(d.lines); n > rows {
		info += itoa(100*min(n, d.scroll+rows)/n) + "%  ·  "
	}
	info += tilde(d.path)
	title := paint(cInk, cBg, " ") + paintBold(cGoldHi, cBg, strings.ToUpper(d.title)) + paint(cFaint, cBg, "  "+info)
	out := []string{pad(title, w, cBg), pad(paint(cLine, cBg, " "+strings.Repeat("─", max(0, w-2))), w, cBg)}

	margin := paint(cInk, cBg, "  ")
	switch {
	case d.missing:
		out = append(out, pad(margin+paint(cStuck, cBg, "the file is gone: "+tilde(d.path)), w, cBg))
	case len(d.lines) == 0:
		out = append(out, pad(margin+paint(cMuted, cBg, "the plan is empty"), w, cBg))
	}
	for i := d.scroll; i < len(d.lines) && len(out) < h; i++ {
		out = append(out, pad(margin+d.lines[i], w, cBg))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// docKey handles a key while the viewer is open.
func (a *App) docKey(k key) {
	d := a.doc
	page := max(1, d.bodyRows-2)
	switch {
	case k.code == keyEsc, k.isRune("q"), k.isRune("m"):
		a.doc = nil
	case k.isCtrl("c"):
		a.quit = true
	case k.code == keyDown, k.isRune("j"), k.code == keyEnter:
		d.scroll++
	case k.code == keyUp, k.isRune("k"):
		d.scroll--
	case k.code == keyPgDown, k.isRune(" "), k.isCtrl("d"), k.isRune("f"):
		d.scroll += page
	case k.code == keyPgUp, k.isRune("b"), k.isCtrl("u"):
		d.scroll -= page
	case k.code == keyHome, k.isRune("g"):
		d.scroll = 0
	case k.code == keyEnd, k.isRune("G"):
		d.scroll = len(d.lines)
	case k.isRune("]"):
		for _, h := range d.heads {
			if h > d.scroll {
				d.scroll = h
				break
			}
		}
	case k.isRune("["):
		prev := 0
		for _, h := range d.heads {
			if h >= d.scroll {
				break
			}
			prev = h
		}
		d.scroll = prev
	case k.isRune("r"):
		a.promptFeedback()
	case k.isRune("?"):
		a.help = true
	}
	d.scroll = max(0, min(d.scroll, len(d.lines)-1))
}

// promptFeedback asks for feedback on the open document, for its session.
func (a *App) promptFeedback() {
	t := a.doc.target
	if i := a.index(t.key); i >= 0 {
		t = a.tiles[i] // the session as it is now: its socket may have changed
	}
	if !t.canReply() {
		a.say("feedback needs a Claude Code session cockpit can message; " + t.name() + " has no messaging socket")
		return
	}
	a.openPrompt(&prompt{kind: promptFeedback, target: t, label: "feedback on the plan"})
}

// feedbackMessage is what the session receives: the feedback, saying which document it is about.
func feedbackMessage(path, text string) string {
	return "Feedback on your plan (" + tilde(path) + "):\n\n" + text
}

func (a *App) docFooterKeys(key func(k, d string) string) string {
	return key("j k", "scroll") + key("space b", "page") + key("[ ]", "section") + key("g G", "top/end") +
		key("r", "feedback") + key("esc", "close")
}
