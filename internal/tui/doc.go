package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
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

// planPaths are where a Claude Code session's plan may be, most likely first: in the plansDirectory its
// settings name (the project's local settings, the project's, then the profile's), and in the profile's
// plans folder, the default. Like Claude Code, a plansDirectory outside the project is not followed.
func planPaths(sessionsDir, cwd, slug string) []string {
	cfg := filepath.Dir(sessionsDir)
	if sessionsDir == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".claude")
	}
	name := slug + ".md"
	var out []string
	if cwd != "" {
		for _, settings := range []string{
			filepath.Join(cwd, ".claude", "settings.local.json"),
			filepath.Join(cwd, ".claude", "settings.json"),
			filepath.Join(cfg, "settings.json"),
		} {
			if dir := plansDirectory(settings); dir != "" {
				if p := filepath.Join(cwd, dir); insideDir(cwd, p) {
					out = append(out, filepath.Join(p, name))
				}
			}
		}
	}
	return append(out, filepath.Join(cfg, "plans", name))
}

// plansDirectory is the plansDirectory a settings file sets, or "".
func plansDirectory(settings string) string {
	b, err := os.ReadFile(settings)
	if err != nil {
		return ""
	}
	var v struct {
		PlansDirectory string `json:"plansDirectory"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return v.PlansDirectory
}

// insideDir reports whether p is dir or below it, once both are cleaned; an absolute or "../" setting
// that leads out of the project is not.
func insideDir(dir, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// validSlug reports whether a plan name read from a transcript is one Claude Code would write: letters,
// digits and dashes, so it cannot lead out of the plans folder.
func validSlug(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
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
	if !validSlug(slug) {
		a.say("not opening the plan: its name in the transcript is not a plan name")
		return
	}
	paths := planPaths(t.sess.SessionsDir, t.sess.Cwd, slug)
	for _, path := range paths {
		if isFile(path) {
			a.doc = &docView{path: path, title: "plan · " + t.name(), target: t}
			return
		}
	}
	a.say("plan not found: " + tilde(paths[len(paths)-1]))
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
		t = a.tiles[i] // the session as it is now: its pane or socket may have changed
	}
	if !(t.livePane() && a.mc != nil) && !t.canReply() {
		a.say("feedback needs a Claude Code session in a pane or with a messaging socket; " + t.name() + " has neither")
		return
	}
	a.openPrompt(&prompt{kind: promptFeedback, target: t, label: "feedback on the plan"})
}

// feedbackGap is the pause between the keystrokes that answer the approval menu: Claude Code has to
// draw the feedback field before text typed into it lands there.
var feedbackGap = 300 * time.Millisecond

// menuChoice matches the approval menu's option for feedback, "❯ 3. Tell Claude what to change".
var menuChoice = regexp.MustCompile(`(\d+)\.\s+Tell Claude what to change`)

// sendFeedback delivers feedback on the open plan to its session. Claude Code holds a message from
// another session whose permission mode differs from its own, so in a pane the feedback is typed,
// as the person would: on the plan's approval menu, into its "Tell Claude what to change" field, and
// otherwise as the next message. A session in a terminal tab gets it through its messaging socket.
func (a *App) sendFeedback(t tile, path, text string) {
	if !t.livePane() || a.mc == nil {
		a.sendReply(t, feedbackMessage(path, text))
		return
	}
	var steps []mux.Msg
	id := t.pane.ID
	if choice := approvalChoice(a.mc.Screen(id)); choice != "" {
		steps = append(steps, mux.Msg{Op: mux.OpInput, ID: id, Data: []byte(choice)},
			mux.Msg{Op: mux.OpPaste, ID: id, Data: []byte(text)})
	} else {
		steps = append(steps, mux.Msg{Op: mux.OpPaste, ID: id, Data: []byte(feedbackMessage(path, text))})
	}
	steps = append(steps, mux.Msg{Op: mux.OpInput, ID: id, Data: []byte("\r")})
	delete(a.scrollBack, id)
	mc, name, gap := a.mc, t.name(), feedbackGap
	a.goSafe(func() {
		for i, m := range steps {
			if i > 0 {
				time.Sleep(gap)
			}
			if err := mc.Send(m); err != nil {
				a.post(event{kind: evFlash, msg: "feedback failed: " + err.Error()})
				return
			}
		}
		a.post(event{kind: evFlash, msg: "feedback typed into " + name})
	})
}

// approvalChoice is the number to press for "Tell Claude what to change" when the plan's approval menu
// is on the pane's screen, or "".
func approvalChoice(scr *mux.Screen) string {
	if scr == nil {
		return ""
	}
	for _, l := range scr.Lines {
		if m := menuChoice.FindStringSubmatch(ansi.Strip(l)); m != nil {
			return m[1]
		}
	}
	return ""
}

// feedbackMessage is what the session receives: the feedback, saying which document it is about.
func feedbackMessage(path, text string) string {
	return "Feedback on your plan (" + tilde(path) + "):\n\n" + text
}

func (a *App) docFooterKeys(key func(k, d string) string) string {
	return key("j k", "scroll") + key("space b", "page") + key("[ ]", "section") + key("g G", "top/end") +
		key("r", "feedback") + key("esc", "close")
}
