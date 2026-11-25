package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
)

// fakeServer records what the dashboard sends instead of talking to `cockpit serve`. Like the real
// client, it is safe to Send from any goroutine: moving a session sends from a background goroutine.
type fakeServer struct {
	mu    sync.Mutex
	panes []mux.PaneInfo
	sent  []mux.Msg
}

func (f *fakeServer) Send(m mux.Msg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}
func (f *fakeServer) Panes() []mux.PaneInfo     { return f.panes }
func (f *fakeServer) Screen(string) *mux.Screen { return nil }
func (f *fakeServer) Close() error              { return nil }
func (f *fakeServer) ops(op string) []mux.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return filterOps(f.sent, op)
}

func filterOps(ms []mux.Msg, op string) (out []mux.Msg) {
	for _, m := range ms {
		if m.Op == op {
			out = append(out, m)
		}
	}
	return out
}

// testDir holds the files the tests write, such as each test app's to-do list; TestMain removes it.
var testDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cockpit-tui-test")
	if err != nil {
		panic(err)
	}
	testDir = dir
	canFocusTabs = true // the tests describe macOS, where tabs can be reached; TestKeysWithoutTabs covers the rest
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newTestApp is a dashboard of w×h whose to-do list is a file of its own, never the person's.
func newTestApp(view viewKind, w, h int) *App {
	a := newApp(nil, Options{Grid: view == gridView})
	a.w, a.h = w, h
	a.loading = false
	dir, err := os.MkdirTemp(testDir, "app")
	if err != nil {
		panic(err)
	}
	a.tasks = &todo.Store{Path: filepath.Join(dir, "todo.json")}
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
	press(a, "x") // close the pane? y·⏎ yes · n·esc no
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

func TestEnterConfirmsEscDeclines(t *testing.T) {
	a, fs := appWithPane()
	press(a, "x")
	a.onKeys([]key{{code: keyEsc}})
	if a.mode != navMode || len(fs.ops(mux.OpClose)) != 0 {
		t.Fatal("esc did not decline")
	}
	press(a, "x", "\r")
	if a.mode != navMode || len(fs.ops(mux.OpClose)) != 1 {
		t.Fatal("enter did not confirm")
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

// recordStops replaces stopping an agent with a record of which pids would have been stopped.
func recordStops(a *App) chan int {
	stopped := make(chan int, 4)
	a.stop = func(s model.Session) error { stopped <- s.PID; return nil }
	return stopped
}

func snapshot(a *App, sessions ...model.Session) {
	a.handle(event{kind: evSnapshot, snap: model.Snapshot{Sessions: sessions}})
}

func TestContinuingABusySessionWaitsForItsTurnToEnd(t *testing.T) {
	a, fs, _ := appWithTabSession()
	stopped := recordStops(a)
	busy := a.snap.Sessions[0]
	busy.State = model.Busy
	snapshot(a, busy)

	press(a, "\r")
	if !strings.Contains(a.prompt.label, "moves when this turn ends") {
		t.Fatalf("question: %q", a.prompt.label)
	}
	press(a, "y")
	snapshot(a, busy) // still working: nothing happens
	select {
	case pid := <-stopped:
		t.Fatalf("stopped pid %d mid-turn", pid)
	case <-time.After(100 * time.Millisecond):
	}
	if !a.isMovePending(a.tiles[0]) {
		t.Fatal("the move is not pending")
	}

	idle := busy
	idle.State = model.Idle
	snapshot(a, idle) // the turn ended: now it moves
	select {
	case pid := <-stopped:
		if pid != 4321 {
			t.Errorf("stopped pid %d", pid)
		}
	case <-time.After(time.Second):
		t.Fatal("did not move after the turn ended")
	}
	waitFor(t, func() bool { return len(fs.ops(mux.OpSpawn)) == 1 })
	if spec := fs.ops(mux.OpSpawn)[0]; strings.Join(spec.Args, " ") != "--resume s1" || spec.Cwd != "/srv/api" {
		t.Errorf("spawn = %+v", spec)
	}
	if len(a.pendingMoves) != 0 {
		t.Error("the move stayed pending")
	}
}

func TestPendingMoveCanBeCancelledAndIsDroppedWhenTheSessionEnds(t *testing.T) {
	a, _, _ := appWithTabSession()
	stopped := recordStops(a)
	busy := a.snap.Sessions[0]
	busy.State = model.Busy
	snapshot(a, busy)

	press(a, "\r", "y", "\r") // schedule, then enter again on the same card
	if !strings.HasPrefix(a.prompt.label, "cancel continuing api here?") {
		t.Fatalf("question: %q", a.prompt.label)
	}
	press(a, "y")
	idle := busy
	idle.State = model.Idle
	snapshot(a, idle)
	select {
	case <-stopped:
		t.Fatal("a cancelled move ran")
	case <-time.After(100 * time.Millisecond):
	}

	press(a, "\r", "y") // idle now: it would move at once, so make it busy first
	snapshot(a, busy)
	press(a, "\r", "y")
	snapshot(a) // the session ended in its tab
	if len(a.pendingMoves) != 0 {
		t.Fatal("a move for a session that ended stayed pending")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met")
}

func TestEnterReopensAnEndedPane(t *testing.T) {
	fs := &fakeServer{panes: []mux.PaneInfo{
		{ID: "p1", Agent: "claude", Cwd: "/srv/api", PID: 99},
		{ID: "p2", Agent: "shell", Cwd: "/tmp", PID: 98},
	}}
	a := newTestApp(gridView, 160, 40)
	a.mc = fs
	a.snap.Sessions = []model.Session{{ID: "claude:s1", Agent: "claude", Name: "api", PID: 99, Cwd: "/srv/api",
		NativeID: "s1", State: model.Idle, Since: time.Now()}}
	a.rebuild()

	// Both programs exit; the session goes away with its process.
	fs.panes[0].Exited, fs.panes[1].Exited = true, true
	a.snap.Sessions = nil
	a.rebuild()
	a.relayout()

	a.sel = paneKey("p1")
	press(a, "\r")
	a.sel = paneKey("p2")
	press(a, "\r")
	closed, spawned := fs.ops(mux.OpClose), fs.ops(mux.OpSpawn)
	if len(closed) != 2 || closed[0].ID != "p1" || closed[1].ID != "p2" {
		t.Fatalf("closed %+v", closed)
	}
	if len(spawned) != 2 {
		t.Fatalf("spawned %+v", spawned)
	}
	if s := spawned[0]; s.Agent != "claude" || s.Cwd != "/srv/api" || strings.Join(s.Args, " ") != "--resume s1" {
		t.Errorf("claude pane reopened as %+v, want its conversation resumed", s)
	}
	if s := spawned[1]; s.Agent != "shell" || s.Cwd != "/tmp" || len(s.Args) != 0 {
		t.Errorf("shell pane reopened as %+v", s)
	}
}

func TestFooterAndHelp(t *testing.T) {
	a, _, _ := appWithTabSession()
	lines, _ := a.compose()
	foot := ansi.Strip(lines[len(lines)-1])
	if !strings.HasPrefix(foot, " ⏎ continue here   o go to tab") || !strings.Contains(foot, "? keys") {
		t.Errorf("footer: %q", foot)
	}
	press(a, "?")
	lines, _ = a.compose()
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "KEYS") {
		t.Fatal("? did not show the keys")
	}
	press(a, "j")
	if a.help || a.cur() != 0 {
		t.Errorf("a key on the help screen should only close it: help %v, selection %d", a.help, a.cur())
	}
}

func TestSelectWaitsForTheSession(t *testing.T) {
	a := newTestApp(gridView, 160, 40)
	a.snap.Sessions = testSessions(3)
	a.rebuild()
	a.zoom, a.help = true, true
	a.handle(event{kind: evSelect, msg: "id09"})
	if a.sel == "id09" || a.pendingSelect != "id09" {
		t.Fatalf("selected a session not collected yet: sel %q pending %q", a.sel, a.pendingSelect)
	}
	a.handle(event{kind: evSnapshot, snap: model.Snapshot{Sessions: testSessions(10)}})
	if a.sel != "id09" || a.pendingSelect != "" || a.zoom || a.help {
		t.Errorf("after the session showed up: sel %q pending %q zoom %v help %v", a.sel, a.pendingSelect, a.zoom, a.help)
	}
}
