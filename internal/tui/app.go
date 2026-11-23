// Package tui is the dashboard: raw terminal I/O, layout, drawing and keys. docs/architecture.md
// describes how it fits with the collector and the pane server.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/chat"
	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
	"github.com/pedro-rodiguero/agent-cockpit/internal/term"
)

// Options configures the dashboard.
type Options struct {
	Interval  time.Duration // how often sessions are collected (at least minCollectInterval)
	LabelTabs bool          // write each session's state into its terminal tab title
	Grid      bool          // start in the grid rather than the list
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
}

// paneServer is the part of mux.Client the dashboard uses; tests substitute a fake.
type paneServer interface {
	Send(mux.Msg) error
	Panes() []mux.PaneInfo
	Screen(id string) *mux.Screen
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

	snap  model.Snapshot
	convs map[string]chat.Conversation // session ID → the end of its conversation and what its log says
	panes []mux.PaneInfo
	tiles []tile
	sel   string // key of the selected tile
	lay   layout

	view     viewKind
	onlyAttn bool
	zoom     bool
	mode     mode
	prompt   *prompt

	keys     keyDecoder
	inputSeq uint64 // number of the last terminal read, to tell stale escape timeouts apart
	typed    []byte // keys for the selected pane, sent together after each read

	labels        *labeler
	shownLabels   map[string]string
	resized       map[string][2]int // pane ID → the size last requested for it
	pendingResume string            // how to resume a moved session by hand, until its pane exists
	pendingMoves  map[string]int    // session ID → pid of sessions to move into a pane when their turn ends
	flash         flash

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
		opts:      opts,
		col:       col,
		launchers: launchers(),
		focus:     term.Focus,
		stop:      func(s model.Session) error { return stopAgent(s, runsAgent, stopProcess) },
		resized:   map[string][2]int{},
		events:    make(chan event, eventBuffer),
		done:      make(chan struct{}),
		loading:   true,
		w:         fallbackCols,
		h:         fallbackRows,
	}
	if opts.Grid {
		a.view = gridView
	}
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
		default:
			if a.muxPending.CompareAndSwap(false, true) {
				a.post(event{kind: evPanes})
			}
		}
	}
	a.goSafe(func() {
		_ = mc.Run()
		a.post(event{kind: evFlash, msg: "lost connection to cockpit serve"})
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
		a.post(event{kind: evSnapshot, snap: snap, chats: readChats(&a.chats, snap.Sessions)})
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// readChats returns the end of each agent session's conversation, by session ID.
func readChats(r *chat.Reader, sessions []model.Session) map[string]chat.Conversation {
	chats := map[string]chat.Conversation{}
	for _, s := range sessions {
		if s.Log == "" || s.State == model.Inbox {
			continue
		}
		chats[s.ID] = r.Read(s.Agent, s.Log, chatLinesPerCard)
	}
	return chats
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
		a.snap, a.convs = ev.snap, ev.chats
		a.rebuild()
		a.updateLabels()
		a.runPendingMoves()
	case evPanes:
		a.muxPending.Store(false)
		a.rebuild()
	case evPaneError:
		a.onPaneError(ev.msg)
	case evSpawned:
		a.onSpawned(ev.msg)
	case evAdoptStopped:
		a.pendingResume = ev.msg
	case evFlash:
		a.say(ev.msg)
	case evAlert:
		a.alert(ev.msg)
	case evResize:
		a.w, a.h = a.scr.size()
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
	tiles := buildTiles(a.snap.Sessions, a.panes, a.view)
	if a.onlyAttn {
		keep := ""
		if a.mode == insertMode {
			keep = a.sel
		}
		tiles = needingAttention(tiles, keep)
	}
	a.tiles = tiles
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
	a.lay = computeLayout(a.w, a.h, len(a.tiles), a.view, a.zoom, a.index(a.sel))
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
	n := len(a.tiles) + 1
	return computeLayout(a.w, a.h, n, gridView, false, n-1).innerSize(0)
}

func (a *App) updateLabels() {
	if !a.opts.LabelTabs {
		return
	}
	// Every session counts, not just the ones the attention filter shows.
	changes, next := labelDiff(a.shownLabels, wantedLabels(buildTiles(a.snap.Sessions, a.panes, a.view)))
	a.shownLabels = next
	a.labels.set(changes)
}

func (a *App) clearLabels() {
	changes, _ := labelDiff(a.shownLabels, nil)
	a.shownLabels = nil
	a.labels.set(changes)
}
