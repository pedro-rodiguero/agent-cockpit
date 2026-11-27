package mux

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// IdleExit is how long the server lingers with no panes and no dashboards before exiting.
const IdleExit = 10 * time.Minute

// frameInterval caps screen updates at about 30 per second per pane.
const frameInterval = 33 * time.Millisecond

// Minimum pane size; smaller requests are clamped so agents never see a degenerate terminal.
const (
	minCols = 20
	minRows = 5
)

// Server owns every pane: the agent process, its PTY and the emulator that keeps its screen.
type Server struct {
	mu      sync.Mutex
	panes   map[string]*pane
	order   []string // pane ids in creation order
	clients map[*client]bool
	nextID  int
	changed bool // the pane list changed since the last broadcast
	lastUse time.Time
	stop    func() // ends the process for OpShutdown; nil in tests
}

type pane struct {
	term terminal // the program and the pseudo-terminal it runs on
	emu  *vt.SafeEmulator
	osc  oscFilter // used by pump only

	mu     sync.Mutex // guards info, dirty, paste, mouse, lastIO and redraw
	info   PaneInfo
	dirty  bool // the screen changed since the last broadcast
	paste  pasteMode
	mouse  mouseMode
	lastIO time.Time // the latest input or output; info.LastIO follows it within ioGranularity
	redraw time.Time // output until then is the program redrawing after a resize, not activity
}

type client struct {
	conn net.Conn
	out  chan Msg
	done chan struct{}
}

func newServer() *Server {
	return &Server{panes: map[string]*pane{}, clients: map[*client]bool{}, lastUse: time.Now()}
}

// Serve listens on SocketPath until the process exits. It refuses to start when another server is
// already answering on the socket, and exits on its own after IdleExit with nothing to do.
func Serve() error {
	if err := ensurePrivateDir(Dir()); err != nil {
		return err
	}
	sock := SocketPath()
	if len(sock) > maxSocketPath {
		return fmt.Errorf("socket path %s is longer than the %d bytes a Unix socket allows; set TMPDIR to a shorter folder", sock, maxSocketPath)
	}
	if err := ensurePrivateDir(filepath.Dir(sock)); err != nil {
		return err
	}
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		c.Close()
		return errors.New("already running")
	}
	_ = os.Remove(sock) // stale socket from a server that died
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	s := newServer()
	s.stop = func() {
		log.Print("shutting down: asked to")
		_ = os.Remove(sock)
		os.Exit(0)
	}
	go s.broadcast()
	go s.watchForeground()
	go s.exitWhenIdle(sock)
	log.Printf("listening on %s", sock)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		if err := samePeer(conn); err != nil {
			log.Printf("refused a connection: %v", err)
			conn.Close()
			continue
		}
		go s.handle(conn)
	}
}

func (s *Server) exitWhenIdle(sock string) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		idle := len(s.panes) == 0 && len(s.clients) == 0 && time.Since(s.lastUse) > IdleExit
		s.mu.Unlock()
		if idle {
			log.Print("idle, exiting")
			_ = os.Remove(sock)
			os.Exit(0)
		}
	}
}

// handle serves one dashboard connection until it closes.
func (s *Server) handle(conn net.Conn) {
	c := &client{conn: conn, out: make(chan Msg, 256), done: make(chan struct{})}
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.lastUse = time.Now()
		s.mu.Unlock()
		close(c.done)
		conn.Close()
	}()
	go c.writeLoop()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var m Msg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			log.Printf("bad frame: %v", err)
			continue
		}
		switch m.Op {
		case OpAttach:
			s.attach(c)
		case OpSpawn:
			id, err := s.spawn(m)
			if err != nil {
				c.send(Msg{Op: OpError, Err: err.Error()})
				continue
			}
			c.send(Msg{Op: OpSpawned, ID: id})
		case OpInput:
			if p := s.get(m.ID); p != nil && !p.exited() {
				p.active()
				_, _ = p.term.Write(m.Data)
			}
		case OpPaste:
			if p := s.get(m.ID); p != nil && !p.exited() {
				p.mu.Lock()
				data := p.paste.wrap(m.Data)
				p.lastIO = time.Now()
				p.mu.Unlock()
				_, _ = p.term.Write(data)
			}
		case OpWheel:
			if p := s.get(m.ID); p != nil && !p.exited() {
				alt := p.emu.IsAltScreen()
				p.mu.Lock()
				data := p.mouse.wheel(m.Up, m.X, m.Y, alt)
				p.mu.Unlock()
				if data != nil {
					_, _ = p.term.Write(data)
				}
			}
		case OpResize:
			if p := s.get(m.ID); p != nil {
				p.resize(m.Cols, m.Rows)
				s.touch()
			}
		case OpClose:
			s.close(m.ID)
		case OpHistory:
			if p := s.get(m.ID); p != nil {
				c.send(Msg{Op: OpHistory, Screen: p.history(m.Rows)})
			}
		case OpShutdown:
			if s.running() > 0 {
				c.send(Msg{Op: OpError, Err: "not shutting down: panes are running"})
				continue
			}
			if s.stop != nil {
				s.stop()
			}
		case OpSelect:
			s.mu.Lock()
			for d := range s.clients {
				d.send(m)
			}
			s.mu.Unlock()
		}
	}
}

// attach subscribes c to broadcasts and sends it the current state in full.
func (s *Server) attach(c *client) {
	s.mu.Lock()
	s.clients[c] = true
	s.lastUse = time.Now()
	list := s.listLocked()
	panes := make([]*pane, 0, len(s.order))
	for _, id := range s.order {
		panes = append(panes, s.panes[id])
	}
	s.mu.Unlock()
	c.send(Msg{Op: OpHello, Version: ProtocolVersion})
	c.send(Msg{Op: OpPanes, Panes: list})
	for _, p := range panes {
		c.send(Msg{Op: OpScreen, Screen: p.screen()})
	}
}

func (c *client) writeLoop() {
	enc := json.NewEncoder(c.conn)
	for {
		select {
		case m := <-c.out:
			if err := enc.Encode(m); err != nil {
				c.conn.Close()
				return
			}
		case <-c.done:
			return
		}
	}
}

// send queues m without blocking. A dashboard that falls behind loses frames rather than stalling the
// server; screens are full snapshots, so the next one repairs the gap. It reports whether m was queued.
func (c *client) send(m Msg) bool {
	select {
	case c.out <- m:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

func (s *Server) get(id string) *pane {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panes[id]
}

// touch marks the pane list as changed so the next broadcast resends it.
func (s *Server) touch() {
	s.mu.Lock()
	s.changed = true
	s.mu.Unlock()
}

func (s *Server) listLocked() []PaneInfo {
	out := make([]PaneInfo, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.panes[id].snapshot())
	}
	return out
}

func (s *Server) spawn(m Msg) (string, error) {
	argv, ok := Agents[m.Agent]
	if !ok {
		return "", fmt.Errorf("unknown agent %q", m.Agent)
	}
	cwd := m.Cwd
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", cwd)
	}
	cols, rows := max(minCols, m.Cols), max(minRows, m.Rows)

	env := append(append(paneEnv(), m.Env...), "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=agent-cockpit")
	f, err := startTerminal(append(append([]string{}, argv...), m.Args...), cwd, env, cols, rows)
	if err != nil {
		return "", err
	}
	emu := vt.NewSafeEmulator(cols, rows)
	emu.SetDefaultForegroundColor(DefaultFg)
	emu.SetDefaultBackgroundColor(DefaultBg)
	emu.SetScrollbackSize(5000)

	s.mu.Lock()
	s.nextID++
	id := strconv.Itoa(s.nextID)
	now := time.Now()
	p := &pane{term: f, emu: emu, lastIO: now, info: PaneInfo{
		ID: id, Agent: m.Agent, Cwd: cwd, PID: f.PID(), Cols: cols, Rows: rows,
		Started: now, Title: filepath.Base(cwd), LastIO: now,
	}}
	s.panes[id] = p
	s.order = append(s.order, id)
	s.changed = true
	s.mu.Unlock()

	setTitle := func(t string) {
		p.mu.Lock()
		p.info.Title = t
		p.mu.Unlock()
		s.touch()
	}
	p.osc.title = setTitle
	emu.SetCallbacks(vt.Callbacks{
		Title: setTitle,
		Bell: func() {
			p.mu.Lock()
			p.info.Bell = time.Now()
			p.mu.Unlock()
			s.touch()
		},
	})
	// The emulator answers terminal queries (cursor position, device attributes, colors); the
	// answers have to reach the program or some agents wait for them forever.
	go func() { _, _ = io.Copy(f, emu) }()
	go s.pump(p)
	return id, nil
}

// pump feeds PTY output into the emulator until the program exits, then records its exit code.
func (s *Server) pump(p *pane) {
	buf := make([]byte, 64<<10)
	for {
		n, err := p.term.Read(buf)
		if n > 0 {
			_, _ = p.emu.Write(p.osc.feed(buf[:n]))
			p.mu.Lock()
			p.dirty = true
			if now := time.Now(); now.After(p.redraw) {
				p.lastIO = now
			}
			p.paste.observe(buf[:n])
			p.mouse.observe(buf[:n])
			p.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code := p.term.Wait()
	p.mu.Lock()
	p.info.Exited, p.info.ExitCode, p.dirty = true, code, true
	p.mu.Unlock()
	s.touch()
}

func (p *pane) snapshot() PaneInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

func (p *pane) exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Exited
}

func (p *pane) resize(cols, rows int) {
	cols, rows = max(minCols, cols), max(minRows, rows)
	p.mu.Lock()
	same := cols == p.info.Cols && rows == p.info.Rows
	exited := p.info.Exited
	p.info.Cols, p.info.Rows = cols, rows
	p.dirty = true
	if !same {
		p.redraw = time.Now().Add(resizeRedraw)
	}
	p.mu.Unlock()
	if same {
		return
	}
	p.emu.Resize(cols, rows)
	if !exited {
		_ = p.term.Resize(cols, rows)
	}
}

func (p *pane) screen() *Screen {
	p.mu.Lock()
	id, cols, rows := p.info.ID, p.info.Cols, p.info.Rows
	p.dirty = false
	p.mu.Unlock()
	pos := p.emu.CursorPosition()
	return &Screen{
		ID: id, Lines: renderLines(p.emu, cols, rows),
		CX: pos.X, CY: pos.Y, Cursor: !p.emu.CursorHidden(), AltMode: p.emu.IsAltScreen(),
	}
}

// history is the pane's screen as it was back lines further up: the scrollback above the live screen,
// then the screen itself. back is clamped to the scrollback's length.
func (p *pane) history(back int) *Screen {
	p.mu.Lock()
	id, cols, rows := p.info.ID, p.info.Cols, p.info.Rows
	p.mu.Unlock()
	n := p.emu.ScrollbackLen()
	back = max(0, min(back, n))
	top := n - back
	cell := func(x, y int) *uv.Cell {
		if y += top; y < n {
			return p.emu.ScrollbackCellAt(x, y)
		}
		return p.emu.CellAt(x, y-n)
	}
	return &Screen{ID: id, Lines: renderCells(cell, cols, rows), Back: back, MaxBack: n}
}

// close removes a pane and stops its program: SIGHUP first, as closing a terminal would, then
// SIGKILL if it is still around three seconds later.
func (s *Server) close(id string) {
	s.mu.Lock()
	p := s.panes[id]
	if p == nil {
		s.mu.Unlock()
		return
	}
	delete(s.panes, id)
	for i, x := range s.order {
		if x == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.changed = true
	s.lastUse = time.Now()
	s.mu.Unlock()
	if !p.exited() {
		p.term.Hangup()
		go func() {
			time.Sleep(3 * time.Second)
			if !p.exited() {
				p.term.Kill()
			}
		}()
	}
	_ = p.term.Close()
}

// broadcast pushes changed screens and pane-list changes to every attached dashboard.
func (s *Server) broadcast() {
	t := time.NewTicker(frameInterval)
	defer t.Stop()
	for range t.C {
		s.broadcastOnce()
	}
}

// broadcastOnce sends the pane list when it changed and the screen of every pane that changed. A
// screen a client had no room for is sent again on the next tick: the last screen of a burst, such as
// the one that shows the cursor again after a program redrew, must not be lost while the pane is quiet.
func (s *Server) broadcastOnce() {
	s.mu.Lock()
	if len(s.clients) == 0 {
		s.mu.Unlock()
		return
	}
	var dirty []*pane
	for _, id := range s.order {
		p := s.panes[id]
		p.mu.Lock()
		if p.dirty {
			dirty = append(dirty, p)
		}
		p.mu.Unlock()
	}
	changed := s.changed
	s.changed = false
	list := s.listLocked()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	screens := make([]Msg, 0, len(dirty))
	for _, p := range dirty {
		screens = append(screens, Msg{Op: OpScreen, Screen: p.screen()})
	}
	for _, c := range clients {
		if changed && !c.send(Msg{Op: OpPanes, Panes: list}) {
			s.touch() // the list is not a snapshot that repeats on its own; retry next tick
		}
		for i, m := range screens {
			if !c.send(m) {
				dirty[i].mu.Lock()
				dirty[i].dirty = true
				dirty[i].mu.Unlock()
			}
		}
	}
}

// paneEnv is the server's environment minus whatever describes the process that happened to start the
// server. Started from inside a Claude Code session, the server inherits that session's markers
// (CLAUDECODE, CLAUDE_CODE_SESSION_ID, CLAUDE_CODE_CHILD_SESSION, ...); a Claude launched with them
// believes it is that session's child and skips registering itself in <config>/sessions, which makes it
// invisible to cockpit. The profile (CLAUDE_CONFIG_DIR) is dropped too, since each spawn sets its own.
// Settings a user exports on purpose come back from their shell profile, because panes start in a
// login shell.
func paneEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE_CODE_") {
			continue
		}
		switch k {
		case "CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_CONFIG_DIR",
			"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// running is how many panes have a program still running.
func (s *Server) running() int {
	s.mu.Lock()
	panes := make([]*pane, 0, len(s.panes))
	for _, p := range s.panes {
		panes = append(panes, p)
	}
	s.mu.Unlock()
	n := 0
	for _, p := range panes {
		if !p.exited() {
			n++
		}
	}
	return n
}

func (p *pane) active() {
	p.mu.Lock()
	p.lastIO = time.Now()
	p.mu.Unlock()
}
