// Package tui is the dashboard: raw terminal I/O, layout, drawing and keys. docs/architecture.md
// describes how it fits with the collector and the pane server.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/config"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todo"
	"github.com/pedro-rodiguero/agent-cockpit/internal/todosync"
)

// Options configures the dashboard.
type Options struct {
	Interval  time.Duration // how often sessions are collected (at least minCollectInterval)
	LabelTabs bool          // write each session's state into its terminal tab title
	Grid      bool          // start in the grid rather than the list
	Settings  *config.File  // the settings file; nil means the defaults
	Select    string        // a session ID to select once it shows up
}

const (
	frameInterval      = 33 * time.Millisecond // drawing happens at most this often (~30 fps)
	clockInterval      = time.Second           // redraw at least this often, for the clock and ages
	collectTimeout     = 6 * time.Second
	minCollectInterval = 250 * time.Millisecond
	escTimeout         = 50 * time.Millisecond // a lone ESC followed by nothing for this long is the Esc key
	flashDuration      = 6 * time.Second
	alertDuration      = time.Minute
	eventBuffer        = 256
	readBufSize        = 64 << 10
	chatLinesPerCard   = 160 // more than the tallest card shows
	reconnectDelay     = 500 * time.Millisecond
)

type mode int

const (
	navMode    mode = iota // keys drive the dashboard
	insertMode             // keys go to the selected pane
	promptMode             // keys edit the footer prompt
)

type eventKind int

const (
	evInput        eventKind = iota // data: bytes read from the terminal
	evEscTimeout                    // seq: no input followed read number seq
	evSnapshot                      // snap, chats: a finished collection pass
	evPanes                         // the pane server's panes or screens changed
	evPaneError                     // msg: the pane server rejected a request
	evSpawned                       // msg: the ID of a new pane
	evAdoptStopped                  // msg: a moved session's process ended; the command that resumes it
	evFlash                         // msg: a message for the footer
	evAlert                         // msg: a message the person must not miss
	evSelect                        // msg: a session ID to select (cockpit select)
	evMuxLost                       // mc: a connection to the pane server ended
	evMuxReplaced                   // an old pane server has exited; connect to a new one
	evResize                        // the window changed size
	evQuit                          // a termination signal arrived
)

type event struct {
	kind  eventKind
	data  []byte
	seq   uint64
	snap  model.Snapshot
	chats map[string]chat.Conversation
	msg   string
	mc    paneServer    // evMuxLost: the connection that ended
	look  appearance    // evSnapshot: the appearance the settings ask for
	quiet time.Duration // evSnapshot: how long a session idles before it moves to the rail
	sync  string        // evSnapshot: why the to-do list's last sync failed, if it did
}

// paneServer is the part of mux.Client the dashboard uses; tests substitute a fake.
type paneServer interface {
	Send(mux.Msg) error
	Panes() []mux.PaneInfo
	Screen(id string) *mux.Screen
	History(id string) *mux.Screen
	Close() error
}

// flash is the message shown in the footer until it expires. An alert replaces the key hints.
type flash struct {
	text  string
	until time.Time
	alert bool
}

// App is the dashboard's state. Only the event loop touches it; other goroutines post events.
type App struct {
	opts      Options
	launchers []launcher
	col       *source.Collector
	chats     chat.Reader               // used by the collect goroutine only
	mc        paneServer                // nil when the pane server could not be reached
	focus     func(tty string) error    // brings a terminal tab to the front; term.Focus outside tests
	stop      func(model.Session) error // stops a session's agent before it is resumed in a pane
	muxErr    string

	look     appearance       // the theme in use
	lookFrom appearanceReader // used by the collect goroutine only

	snap  model.Snapshot
	convs map[string]chat.Conversation // session ID → the end of its conversation and what its log says
	panes []mux.PaneInfo
	tiles []tile
	railN int    // how many tiles at the end of tiles belong in the rail
	sel   string // key of the selected tile
	lay   layout
	// The selection last held in the grid and in the rail, so moving between them returns to it.
	gridSel, railSel string

	view     viewKind
	onlyAttn bool
	zoom     bool
	help     bool // the key list covers the body
	mode     mode
	prompt   *prompt

	keys     keyDecoder
	inputSeq uint64 // number of the last terminal read, to tell stale escape timeouts apart
	typed    []byte // keys for the selected pane, sent together after each read

	labels        *labeler
	shownLabels   map[string]string
	resized       map[string][2]int        // pane ID → the size last requested for it
	pendingResume string                   // how to resume a moved session by hand, until its pane exists
	pendingMoves  map[string]int           // session ID → pid of sessions to move into a pane when their turn ends
	scrollBack    map[string]int           // pane ID → lines the pane is scrolled up into its history; absent when live
	tasks         *todo.Store              // cockpit's own to-do list
	lastDone      string                   // the task u reopens
	lastDirFile   string                   // holds the directory the last new pane started in
	collectNow    chan struct{}            // a send starts a collection pass without waiting for the interval
	paneSessions  map[string]model.Session // pane ID → the session last seen running in it, to reopen it
	pendingSelect string                   // a session to select once it shows up (cockpit select)
	syncErr       string                   // why the to-do list's last sync failed; empty when it worked
	replacing     bool                     // an old pane server was asked to exit
	saidOld       bool                     // the footer said the pane server is old
	flash         flash
	selecting     *selection // text selected with the mouse, shown until the next key or click

	scr        *screen
	w, h       int
	events     chan event
	done       chan struct{} // closed when the loop has ended
	muxPending atomic.Bool   // an evPanes is queued; further updates need no event of their own
	dirty      bool
	loading    bool
	quit       bool
}

func newApp(col *source.Collector, opts Options) *App {
	a := &App{
		opts:        opts,
		col:         col,
		launchers:   launchers(source.ClaudeDirs(opts.Settings.Current())),
		focus:       term.Focus,
		stop:        func(s model.Session) error { return stopAgent(s, runsAgent, stopProcess) },
		resized:     map[string][2]int{},
		tasks:       &todo.Store{Path: todo.DefaultPath(), Settings: func() todo.Settings { return opts.Settings.Current().Todo }},
		lastDirFile: filepath.Join(filepath.Dir(todo.DefaultPath()), "last-pane-dir"),
		collectNow:  make(chan struct{}, 1),
		events:      make(chan event, eventBuffer),
		done:        make(chan struct{}),
		loading:     true,
		w:           fallbackCols,
		h:           fallbackRows,
	}
	if opts.Grid {
		a.view = gridView
	}
	a.pendingSelect = opts.Select
	return a
}

// Run shows the dashboard until the person quits or the process is told to terminate. Panes keep
// running in the pane server afterwards.
func Run(col *source.Collector, opts Options) error {
	scr, err := openScreen()
	if err != nil {
		return err
	}
	defer restoreOnPanic(scr)
	a := newApp(col, opts)
	a.scr = scr
	a.look = a.lookFrom.read(opts.Settings.Current(), time.Now())
	useTheme(a.look)
	a.w, a.h = scr.size()
	live := a.run()
	scr.close()
	if live > 0 {
		fmt.Printf("cockpit: %d pane(s) still running in the background · run `cockpit` to return\n", live)
	}
	return nil
}

// restoreOnPanic puts the terminal back before a panic propagates, so the trace is readable and the
// shell usable.
func restoreOnPanic(scr *screen) {
	if r := recover(); r != nil {
		if scr != nil {
			scr.close()
		}
		panic(r)
	}
}

// goSafe runs fn on a new goroutine that restores the terminal if fn panics.
func (a *App) goSafe(fn func()) {
	go func() {
		defer restoreOnPanic(a.scr)
		fn()
	}()
}

// run starts the background work, runs the event loop, and returns how many panes are still running.
func (a *App) run() int {
	a.connectMux()
	a.labels = startLabeler(func(err error) {
		a.post(event{kind: evFlash, msg: "could not set tab titles: " + err.Error()})
	})
	ctx, cancel := context.WithCancel(context.Background())
	a.goSafe(a.readInput)
	a.goSafe(func() { a.collectLoop(ctx) })
	stopSignals := a.watchSignals()

	a.loop()

	cancel()
	stopSignals()
	close(a.done)
	if a.opts.LabelTabs {
		a.clearLabels()
	}
	a.labels.close()
	if a.mc == nil {
		return 0
	}
	live := livePanes(a.mc.Panes())
	_ = a.mc.Close()
	return live
}

func (a *App) loop() {
	frame := time.NewTicker(frameInterval)
	defer frame.Stop()
	clock := time.NewTicker(clockInterval)
	defer clock.Stop()
	a.relayout()
	a.dirty = true
	for !a.quit {
		select {
		case ev := <-a.events:
			a.handle(ev)
		case <-frame.C:
			if a.dirty {
				a.draw()
				a.dirty = false
			}
		case <-clock.C:
			a.dirty = true
		}
	}
}

// post delivers ev to the loop, or drops it once the loop has ended.
func (a *App) post(ev event) {
	select {
	case a.events <- ev:
	case <-a.done:
	}
}

func (a *App) connectMux() {
	mc, err := mux.Dial()
	if err != nil {
		a.muxErr = err.Error()
		return
	}
	a.mc = mc
	mc.OnUpdate = func(m mux.Msg) {
		switch m.Op {
		case mux.OpSpawned:
			a.post(event{kind: evSpawned, msg: m.ID})
		case mux.OpError:
			a.post(event{kind: evPaneError, msg: m.Err})
		case mux.OpSelect:
			a.post(event{kind: evSelect, msg: m.ID})
		default:
			if a.muxPending.CompareAndSwap(false, true) {
				a.post(event{kind: evPanes})
			}
		}
	}
	a.goSafe(func() {
		_ = mc.Run()
		a.post(event{kind: evMuxLost, mc: mc})
	})
}

func (a *App) readInput() {
	buf := make([]byte, readBufSize)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			a.post(event{kind: evInput, data: append([]byte(nil), buf[:n]...)})
		}
		if err != nil {
			return
		}
	}
}

// watchSignals turns window resizes and termination signals into events. In raw mode ctrl-c is a key,
// so SIGINT only arrives from outside, like SIGTERM and SIGHUP (the terminal closing).
func (a *App) watchSignals() (stop func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	a.goSafe(func() {
		for sig := range sigs {
			if sig == syscall.SIGWINCH {
				a.post(event{kind: evResize})
			} else {
				a.post(event{kind: evQuit})
			}
		}
	})
	return func() {
		signal.Stop(sigs)
		close(sigs)
	}
}

// collectLoop collects sessions and the end of their conversations until ctx ends. Transcripts are read
// here rather than while drawing, so a frame never waits on the disk.
func (a *App) collectLoop(ctx context.Context) {
	interval := max(a.opts.Interval, minCollectInterval)
	for {
		cctx, cancel := context.WithTimeout(ctx, collectTimeout)
		snap := a.col.Collect(cctx)
		cancel()
		cfg := a.opts.Settings.Current()
		a.post(event{kind: evSnapshot, snap: snap, chats: readChats(&a.chats, snap.Sessions),
			look: a.lookFrom.read(cfg, time.Now()), quiet: cfg.QuietAfter.Or(defaultQuietAfter), sync: a.syncProblem()})
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-a.collectNow:
		}
	}
}

// readChats returns the end of each agent session's conversation, by session ID.
func readChats(r *chat.Reader, sessions []model.Session) map[string]chat.Conversation {
	chats := map[string]chat.Conversation{}
	for _, s := range sessions {
		if s.Log == "" || s.State == model.Inbox || s.State == model.Todo {
			continue
		}
		chats[s.ID] = r.Read(s.Agent, s.Log, chatLinesPerCard)
	}
	return chats
}

// markAsked moves idle sessions whose conversation ends on a question to the asked state. Sources
// report the agent's view (the turn is over); the question means the next move is the person's.
func markAsked(snap model.Snapshot, chats map[string]chat.Conversation) model.Snapshot {
	for i, s := range snap.Sessions {
		if s.State == model.Idle && chats[s.ID].Question() != "" {
			snap.Sessions[i].State = model.Asked
		}
	}
	return snap
}

func (a *App) handle(ev event) {
	a.dirty = true
	switch ev.kind {
	case evInput:
		a.inputSeq++
		a.onKeys(a.keys.feed(ev.data))
		a.armEscTimeout()
	case evEscTimeout:
		if ev.seq == a.inputSeq {
			a.onKeys(a.keys.flush())
		}
	case evSnapshot:
		a.loading = false
		a.snap, a.convs = markAsked(ev.snap, ev.chats), ev.chats
		if ev.quiet > 0 {
			quietAfter = ev.quiet
		}
		a.syncErr = ev.sync
		if ev.look != a.look {
			a.look = ev.look
			useTheme(a.look)
			if a.scr != nil {
				a.scr.repaint()
			}
		}
		a.rebuild()
		a.selectPending()
		a.updateLabels()
		a.runPendingMoves()
	case evPanes:
		a.muxPending.Store(false)
		a.rebuild()
		a.checkServer()
	case evMuxLost:
		// The server exited: another dashboard replaced it, or it crashed. Connecting again starts one.
		if ev.mc == a.mc && !a.replacing {
			a.replacing = true
			a.goSafe(func() {
				time.Sleep(reconnectDelay)
				a.post(event{kind: evMuxReplaced})
			})
		}
	case evMuxReplaced:
		a.reconnectMux()
	case evPaneError:
		a.onPaneError(ev.msg)
	case evSpawned:
		a.onSpawned(ev.msg)
	case evAdoptStopped:
		a.pendingResume = ev.msg
	case evSelect:
		a.pendingSelect = ev.msg
		a.selectPending()
	case evFlash:
		a.say(ev.msg)
	case evAlert:
		a.alert(ev.msg)
	case evResize:
		a.selecting = nil
		a.w, a.h = a.scr.size()
		a.rebuild() // whether tasks have a rail to go to depends on the width
	case evQuit:
		a.quit = true
	}
	a.relayout()
}

// armEscTimeout schedules the resolution of input the decoder could not finish, if any.
func (a *App) armEscTimeout() {
	if !a.keys.pendingEscape() {
		return
	}
	seq := a.inputSeq
	time.AfterFunc(escTimeout, func() { a.post(event{kind: evEscTimeout, seq: seq}) })
}

func (a *App) onSpawned(id string) {
	a.pendingResume = ""
	a.view = gridView
	a.rebuild()
	a.sel = paneKey(id)
	a.mode = insertMode
	a.say("new pane · typing goes to the agent · ctrl-q leaves")
}

// onPaneError reports a failed request. When a moved session's pane failed to start, its process is
// already gone, so the person is told how to resume it by hand.
func (a *App) onPaneError(msg string) {
	if a.pendingResume == "" {
		a.say(msg)
		return
	}
	a.alert(stoppedNotResumed(msg, a.pendingResume))
	a.pendingResume = ""
}

func stoppedNotResumed(cause, command string) string {
	return "the session was stopped but its pane did not start (" + cause + ") · resume it with: " + command
}

func (a *App) say(s string) { a.flash = flash{text: s, until: time.Now().Add(flashDuration)} }

func (a *App) alert(s string) {
	a.flash = flash{text: s, until: time.Now().Add(alertDuration), alert: true}
}

// send writes m to the pane server and reports a failure in the footer.
func (a *App) send(m mux.Msg) bool {
	if err := a.mc.Send(m); err != nil {
		a.say("pane server: " + err.Error())
		return false
	}
	return true
}

// rebuild merges the latest sessions and panes into tiles and keeps the selection and mode valid.
func (a *App) rebuild() {
	if a.mc != nil {
		a.panes = a.mc.Panes()
	}
	now := time.Now()
	tiles := buildTiles(a.snap.Sessions, a.panes, a.view, now)
	if a.onlyAttn {
		keep := ""
		if a.mode == insertMode {
			keep = a.sel
		}
		tiles = needingAttention(tiles, keep)
	}
	if a.view == gridView && a.w < minRailWindow {
		tiles = withoutTasks(tiles)
	}
	a.tiles = tiles
	a.rememberPaneSessions()
	a.railN = 0
	if a.view == gridView {
		a.railN = railCount(tiles, now)
	}
	a.forgetClosedPanes()
	if a.index(a.sel) < 0 {
		a.sel = ""
		if len(tiles) > 0 {
			a.sel = tiles[0].key
		}
	}
	if t, ok := a.selected(); a.mode == insertMode && (!ok || !t.livePane()) {
		a.mode = navMode
	}
}

func (a *App) forgetClosedPanes() {
	for id := range a.resized {
		if !a.hasPane(id) {
			delete(a.resized, id)
		}
	}
	for id := range a.paneSessions {
		if !a.hasPane(id) {
			delete(a.paneSessions, id)
		}
	}
}

// rememberPaneSessions records which session runs in each pane. Once a pane's program exits its session
// is gone from the snapshot, and this is how ⏎ on the ended pane knows which conversation to resume.
func (a *App) rememberPaneSessions() {
	for _, t := range a.tiles {
		if t.pane == nil || t.sess == nil {
			continue
		}
		if a.paneSessions == nil {
			a.paneSessions = map[string]model.Session{}
		}
		a.paneSessions[t.pane.ID] = *t.sess
	}
}

func (a *App) hasPane(id string) bool {
	for _, p := range a.panes {
		if p.ID == id {
			return true
		}
	}
	return false
}

// relayout recomputes the layout for the current state and resizes visible panes to their cards.
func (a *App) relayout() {
	a.lay = computeLayout(a.w, a.h, len(a.tiles), a.railN, a.view, a.zoom, a.index(a.sel), a.lay.page)
	a.syncPaneSizes()
}

func (a *App) syncPaneSizes() {
	if a.mc == nil || a.view != gridView || a.w < minWindowCols || a.h < minWindowRows {
		return
	}
	for k := 0; k < a.lay.count; k++ {
		t := a.tiles[a.lay.first+k]
		if !t.livePane() {
			continue
		}
		cols, rows := a.lay.innerSize(k)
		want := [2]int{cols, rows}
		if (t.pane.Cols == cols && t.pane.Rows == rows) || a.resized[t.pane.ID] == want {
			continue
		}
		a.resized[t.pane.ID] = want
		a.send(mux.Msg{Op: mux.OpResize, ID: t.pane.ID, Cols: cols, Rows: rows})
	}
}

// newPaneSize is the terminal size a new pane will most likely get: its card in the grid once it has
// been added. The card size is corrected by a resize as soon as the pane shows up.
func (a *App) newPaneSize() (cols, rows int) {
	return computeLayout(a.w, a.h, len(a.tiles)+1, a.railN, gridView, false, 0, 0).innerSize(0)
}

func (a *App) updateLabels() {
	if !a.opts.LabelTabs {
		return
	}
	// Every session counts, not just the ones the attention filter shows.
	changes, next := labelDiff(a.shownLabels, wantedLabels(buildTiles(a.snap.Sessions, a.panes, a.view, time.Now())))
	a.shownLabels = next
	a.labels.set(changes)
}

func (a *App) clearLabels() {
	changes, _ := labelDiff(a.shownLabels, nil)
	a.shownLabels = nil
	a.labels.set(changes)
}

// selectPending selects the session cockpit select asked for, in the grid or the rail, leaving whatever
// covered it (a pane being typed into, the key list, a zoom on another card). A session not collected
// yet stays pending until it is, so a dashboard just started can be told what to show.
func (a *App) selectPending() {
	id := a.pendingSelect
	if id == "" {
		return
	}
	for _, t := range a.tiles {
		if t.key == id || (t.sess != nil && t.sess.ID == id) {
			a.pendingSelect = ""
			a.sel = t.key
			a.mode, a.help, a.zoom, a.prompt = navMode, false, false, nil
			return
		}
	}
}

// checkServer replaces a pane server older than this dashboard, which ignores requests it does not
// know, such as selecting a session from a notification. Replacing it ends its panes, so it waits until
// no pane's program is running; until then the footer says why some things do not work.
func (a *App) checkServer() {
	c, ok := a.mc.(*mux.Client)
	if !ok || a.replacing {
		return
	}
	v, known := c.ServerVersion()
	if !known || v >= mux.ProtocolVersion {
		return
	}
	switch {
	case v < 1: // from before versions: it cannot be asked to exit
		if !a.saidOld {
			a.alert("cockpit serve is an older version · restart it when no pane is busy: pkill -f 'cockpit serve'")
		}
		a.saidOld = true
	case livePanes(a.panes) > 0:
		if !a.saidOld {
			a.alert("cockpit serve is an older version · it restarts on its own when its last pane closes")
		}
		a.saidOld = true
	default:
		a.replacing = true
		if err := c.Send(mux.Msg{Op: mux.OpShutdown}); err != nil {
			a.replacing = false
			return
		}
		a.goSafe(func() {
			// Wait for the old server to let go of the socket, so the new connection starts a new one.
			for i := 0; i < 40; i++ {
				probe, err := mux.Connect()
				if err != nil {
					break
				}
				probe.Close()
				time.Sleep(50 * time.Millisecond)
			}
			a.post(event{kind: evMuxReplaced})
		})
	}
}

// reconnectMux connects to the pane server again after the one it was connected to exited, starting a
// new one if none is running.
func (a *App) reconnectMux() {
	if a.mc != nil {
		_ = a.mc.Close()
		a.mc = nil
	}
	a.panes = nil
	if a.saidOld {
		a.flash = flash{} // the warning about the old server
	}
	a.connectMux()
	a.replacing, a.saidOld = false, false
	if a.mc == nil {
		a.say("lost connection to cockpit serve: " + a.muxErr)
	} else {
		a.muxErr = ""
	}
	a.rebuild()
}

// syncProblem is why the to-do list's last sync with its server failed, or "" when it worked or the list
// is not connected. Read by the collect goroutine.
func (a *App) syncProblem() string {
	if a.tasks == nil {
		return ""
	}
	paths := todosync.For(a.tasks.Path)
	if _, ok, _ := paths.LoadLink(); !ok {
		return ""
	}
	return paths.LoadStatus().Error
}
