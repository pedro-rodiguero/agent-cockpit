package mux

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Client is a dashboard's connection to `cockpit serve`. It keeps the latest pane list and screens,
// and calls OnUpdate (from its reader goroutine) whenever something arrives.
type Client struct {
	conn     net.Conn
	enc      *json.Encoder
	wmu      sync.Mutex
	mu       sync.Mutex
	panes    []PaneInfo
	screens  map[string]*Screen
	history  map[string]*Screen // the latest answer to a history request, per pane
	version  int                // the server's ProtocolVersion; 0 for a server from before versions
	attached bool               // the first pane list arrived, so version is known
	OnUpdate func(Msg)
}

// ServerVersion is the server's ProtocolVersion, and whether it is known yet: the server announces it
// before the first pane list.
func (c *Client) ServerVersion() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version, c.attached
}

// ErrNotRunning is returned by Connect when no server is listening.
var ErrNotRunning = errors.New("pane server is not running")

// Connect attaches to a running server without starting one.
func Connect() (*Client, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), 300*time.Millisecond)
	if err != nil {
		return nil, ErrNotRunning
	}
	return newClient(conn), nil
}

// Dial connects to the server, starting it in the background first if it isn't running.
func Dial() (*Client, error) {
	if c, err := Connect(); err == nil {
		return c, nil
	}
	if err := startServer(); err != nil {
		return nil, fmt.Errorf("start pane server: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		if c, err := Connect(); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("pane server did not come up; see %s", filepath.Join(Dir(), "serve.log"))
}

func newClient(conn net.Conn) *Client {
	return &Client{conn: conn, enc: json.NewEncoder(conn), screens: map[string]*Screen{}, history: map[string]*Screen{}}
}

// startServer launches `cockpit serve` detached from this terminal (its own session), so closing the
// dashboard's tab does not take the panes with it. COCKPIT_BIN overrides the executable, which tests use
// because os.Executable is the test binary there.
func startServer() error {
	exe, err := os.Executable()
	if v := os.Getenv("COCKPIT_BIN"); v != "" {
		exe, err = v, nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(Dir(), "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Run attaches and reads frames until the connection closes. Set OnUpdate first, then call Run in its
// own goroutine.
func (c *Client) Run() error {
	if err := c.Send(Msg{Op: OpAttach}); err != nil {
		return err
	}
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m Msg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		c.mu.Lock()
		switch m.Op {
		case OpHello:
			c.version = m.Version
		case OpPanes:
			c.attached = true
			c.panes = m.Panes
			alive := map[string]bool{}
			for _, p := range m.Panes {
				alive[p.ID] = true
			}
			for id := range c.screens {
				if !alive[id] {
					delete(c.screens, id)
					delete(c.history, id)
				}
			}
		case OpScreen:
			if m.Screen != nil {
				c.screens[m.Screen.ID] = m.Screen
			}
		case OpHistory:
			if m.Screen != nil {
				c.history[m.Screen.ID] = m.Screen
			}
		}
		c.mu.Unlock()
		if c.OnUpdate != nil {
			c.OnUpdate(m)
		}
	}
	return sc.Err()
}

// Send writes one frame; it is safe for concurrent use.
func (c *Client) Send(m Msg) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(m)
}

// Panes returns a copy of the latest pane list.
func (c *Client) Panes() []PaneInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]PaneInfo(nil), c.panes...)
}

// Screen returns the latest screen of pane id, or nil before the first one arrives.
func (c *Client) Screen(id string) *Screen {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screens[id]
}

// History returns the latest answer to a history request for pane id, or nil.
func (c *Client) History(id string) *Screen {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.history[id]
}

func (c *Client) Close() error { return c.conn.Close() }
