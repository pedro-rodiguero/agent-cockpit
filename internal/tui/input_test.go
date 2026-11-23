package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

// fakeServer records what the dashboard sends instead of talking to `cockpit serve`.
type fakeServer struct {
	panes []mux.PaneInfo
	sent  []mux.Msg
}

func (f *fakeServer) Send(m mux.Msg) error          { f.sent = append(f.sent, m); return nil }
func (f *fakeServer) Panes() []mux.PaneInfo         { return f.panes }
func (f *fakeServer) Screen(string) *mux.Screen     { return nil }
func (f *fakeServer) Close() error                  { return nil }
func (f *fakeServer) ops(op string) (out []mux.Msg) { return filterOps(f.sent, op) }

func filterOps(ms []mux.Msg, op string) (out []mux.Msg) {
	for _, m := range ms {
		if m.Op == op {
			out = append(out, m)
		}
	}
	return out
}

func newTestApp(view viewKind, w, h int) *App {
	a := newApp(nil, Options{Grid: view == gridView})
	a.w, a.h = w, h
	a.loading = false
	return a
}

// appWithPane is a grid with one live pane (selected) followed by two sessions.
func appWithPane() (*App, *fakeServer) {
	fs := &fakeServer{panes: []mux.PaneInfo{{ID: "p1", Agent: "shell", Cwd: "/tmp", PID: 4242}}}
	a := newTestApp(gridView, 160, 40)
	a.mc = fs
	a.snap.Sessions = testSessions(2)
	a.rebuild()
	a.relayout()
	a.sel = paneKey("p1")
	return a, fs
}

func press(a *App, chunks ...string) {
	for _, c := range chunks {
		a.onKeys(a.keys.feed([]byte(c)))
	}
}

func TestInsertModeKeepsKeysAfterLeaving(t *testing.T) {
	a, fs := appWithPane()
	press(a, "\r") // enter types into the pane
	if a.mode != insertMode {
		t.Fatal("enter did not start typing into the pane")
	}
	press(a, "ab\x1b[A\x11l")
	in := fs.ops(mux.OpInput)
	if len(in) != 1 || string(in[0].Data) != "ab\x1b[A" {
		t.Fatalf("pane got %+v", in)
	}
	if a.mode != navMode {
		t.Fatal("ctrl-q did not leave the pane")
	}
	if a.cur() != 1 {
		t.Fatalf("the l after ctrl-q was dropped: selection %d", a.cur())
	}
}

func TestKeysAfterEnterGoToPane(t *testing.T) {
	a, fs := appWithPane()
	press(a, "\rhi")
	in := fs.ops(mux.OpInput)
	if len(in) != 1 || string(in[0].Data) != "hi" {
		t.Fatalf("pane got %+v", in)
	}
}

func TestPasteIntoPaneUsesPasteOp(t *testing.T) {
	a, fs := appWithPane()
	press(a, "i", "\x1b[200~x\ny", "\x1b[201~")
	ps := fs.ops(mux.OpPaste)
	if len(ps) != 1 || string(ps[0].Data) != "x\ny" || ps[0].ID != "p1" {
		t.Fatalf("pastes sent: %+v", ps)
	}
	if len(fs.ops(mux.OpInput)) != 0 {
		t.Fatalf("paste also sent as typed input: %+v", fs.ops(mux.OpInput))
	}
}

func TestPasteNeverConfirms(t *testing.T) {
	a, fs := appWithPane()
	press(a, "x") // close the pane? y / n
	if a.mode != promptMode || a.prompt.kind != promptConfirm {
		t.Fatal("x did not ask for confirmation")
	}
	press(a, "\x1b[200~py\x1b[201~")
	if a.mode != promptMode {
		t.Fatal("a paste answered the confirmation")
	}
	if len(fs.ops(mux.OpClose)) != 0 {
		t.Fatal("a paste closed the pane")
	}
	press(a, "y")
	if len(fs.ops(mux.OpClose)) != 1 {
		t.Fatal("y did not close the pane")
	}
}

func TestPasteInNavModeDoesNothing(t *testing.T) {
	a, fs := appWithPane()
	press(a, "\x1b[200~xpq\x1b[201~")
	if a.mode != navMode || a.quit || len(fs.sent) != len(fs.ops(mux.OpResize)) {
		t.Fatalf("paste acted as keys: mode %v quit %v sent %+v", a.mode, a.quit, fs.sent)
	}
}

func TestPasteIntoPrompt(t *testing.T) {
	a := newTestApp(gridView, 160, 40)
	a.snap.Sessions = []model.Session{{ID: "c", Agent: "claude", Name: "c", Socket: "/s", State: model.Idle}}
	a.rebuild()
	press(a, "r", "\x1b[200~yes\nplease\x1b[201~")
	if a.prompt == nil || string(a.prompt.text) != "yes please" {
		t.Fatalf("prompt = %+v", a.prompt)
	}
}

func TestEscapeTimeoutIsIgnoredWhenStale(t *testing.T) {
	a := newTestApp(gridView, 160, 40)
	a.zoom = true
	a.handle(event{kind: evInput, data: []byte("\x1b")})
	seq := a.inputSeq
	a.handle(event{kind: evInput, data: []byte("[B")}) // the rest of a down arrow
	a.handle(event{kind: evEscTimeout, seq: seq})
	if !a.zoom {
		t.Fatal("a stale timeout turned the start of an arrow key into esc")
	}
	a.handle(event{kind: evInput, data: []byte("\x1b")})
	a.handle(event{kind: evEscTimeout, seq: a.inputSeq})
	if a.zoom {
		t.Fatal("lone esc did not leave zoom")
	}
}

func TestPaneResizesComeFromTheLoop(t *testing.T) {
	a, fs := appWithPane()
	rs := fs.ops(mux.OpResize)
	if len(rs) != 1 || rs[0].ID != "p1" {
		t.Fatalf("resizes after layout: %+v", rs)
	}
	before := len(fs.sent)
	a.compose()
	if len(fs.sent) != before {
		t.Fatal("drawing sent messages")
	}
	a.relayout()
	if len(fs.ops(mux.OpResize)) != 1 {
		t.Fatal("the same size was requested twice")
	}
	fs.panes = nil
	a.rebuild()
	if len(a.resized) != 0 {
		t.Fatalf("closed pane still tracked: %v", a.resized)
	}
}

func TestStartPaneValidatesDirectory(t *testing.T) {
	a, fs := appWithPane()
	a.openPrompt(&prompt{kind: promptNew, text: []rune("/definitely/not/here")})
	a.submit()
	if a.mode != promptMode || len(fs.ops(mux.OpSpawn)) != 0 {
		t.Fatal("spawned in a directory that does not exist")
	}
	dir := t.TempDir()
	a.prompt.text = []rune(dir)
	a.submit()
	sp := fs.ops(mux.OpSpawn)
	if a.mode != navMode || len(sp) != 1 || sp[0].Cwd != dir {
		t.Fatalf("spawns: %+v", sp)
	}
}

func TestAlertOutlivesFlash(t *testing.T) {
	a := newTestApp(gridView, 160, 40)
	a.pendingResume = "cd /x && claude --resume abc"
	a.handle(event{kind: evPaneError, msg: "boom"})
	if !a.flash.alert || a.pendingResume != "" {
		t.Fatalf("flash = %+v", a.flash)
	}
	if time.Until(a.flash.until) <= flashDuration {
		t.Fatal("alert expires like an ordinary message")
	}
}

func TestNavActionIgnoresPasteAndAlt(t *testing.T) {
	for _, k := range []key{
		{code: keyPaste, text: "q"},
		{code: keyPaste, text: "x"},
		{code: keyRune, text: "q", alt: true},
	} {
		if act := navAction(k); act != actNone {
			t.Errorf("navAction(%+v) = %v, want none", k, act)
		}
	}
	if navAction(key{code: keyRune, text: "q"}) != actQuit {
		t.Error("q does not quit")
	}
}

// appWithTabSession is a grid whose selected card is a resumable Claude session in a terminal tab.
// Focusing a tab is recorded on the returned channel instead of driving Terminal.app.
func appWithTabSession() (*App, *fakeServer, chan string) {
	fs := &fakeServer{}
	a := newTestApp(gridView, 160, 40)
	a.mc = fs
	focused := make(chan string, 1)
	a.focus = func(tty string) error { focused <- tty; return nil }
	a.snap.Sessions = []model.Session{{ID: "claude:s1", Agent: "claude", Name: "api", PID: 4321, TTY: "ttys004",
		Cwd: "/srv/api", NativeID: "s1", State: model.Idle}}
	a.rebuild()
	a.relayout()
	a.sel = "claude:s1"
	return a, fs, focused
}

func TestEnterOffersToContinueATabSessionHere(t *testing.T) {
	a, fs, focused := appWithTabSession()
	press(a, "\r")
	if a.mode != promptMode || a.prompt.kind != promptConfirm || !strings.HasPrefix(a.prompt.label, "continue api here?") {
		t.Fatalf("enter did not ask to continue here: mode %v, prompt %+v", a.mode, a.prompt)
	}
	press(a, "n")
	if a.mode != navMode || len(fs.sent) != 0 || len(focused) != 0 {
		t.Fatal("declining did something")
	}

	press(a, "\r", "o")
	select {
	case tty := <-focused:
		if tty != "ttys004" {
			t.Errorf("focused %q", tty)
		}
	case <-time.After(time.Second):
		t.Fatal("o did not go to the tab")
	}
	if a.mode != navMode || len(fs.sent) != 0 {
		t.Fatal("o also moved the session")
	}
}

func TestEnterGoesToTheTabWhenTheSessionCannotMove(t *testing.T) {
	a, _, focused := appWithTabSession()
	a.snap.Sessions[0].NativeID = "" // nothing to resume it by
	a.rebuild()
	press(a, "\r")
	if a.mode != navMode {
		t.Fatalf("asked a question for a session that cannot move: %+v", a.prompt)
	}
	select {
	case <-focused:
	case <-time.After(time.Second):
		t.Fatal("enter did not go to the tab")
	}
}
