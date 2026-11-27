package mux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// shown waits until the pane's screen holds want, and fails with the screen when it never does.
func shown(t *testing.T, p *pane, want string) {
	t.Helper()
	var text string
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		text = ansi.Strip(strings.Join(p.screen().Lines, "\n"))
		if strings.Contains(text, want) {
			return
		}
	}
	t.Fatalf("the screen never showed %q:\n%s", want, text)
}

func ended(t *testing.T, p *pane) int {
	t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if p.exited() {
			return p.snapshot().ExitCode
		}
	}
	t.Fatal("the program never ended")
	return -1
}

func TestPaneShowsWhatAProgramPrintsAndItsExitCode(t *testing.T) {
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 60, Rows: 10, Args: []string{"cmd.exe", "/d", "/c", "echo hello-conpty& exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	shown(t, p, "hello-conpty")
	if code := ended(t, p); code != 3 {
		t.Fatalf("exit code %d, want 3", code)
	}
	s.close(id)
}

func TestPaneTakesTypingAResizeAndAClose(t *testing.T) {
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 60, Rows: 10, Args: []string{"cmd.exe", "/d", "/q", "/k"}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	if _, err := p.term.Write([]byte("echo typed-%OS%\r")); err != nil {
		t.Fatal(err)
	}
	shown(t, p, "typed-Windows_NT")
	p.resize(100, 30)
	if scr := p.screen(); len(scr.Lines) != 30 {
		t.Fatalf("%d rows after the resize", len(scr.Lines))
	}
	if _, err := p.term.Write([]byte("echo after-resize\r")); err != nil {
		t.Fatal(err)
	}
	shown(t, p, "after-resize")
	s.close(id)
	ended(t, p)
}

// An agent installed as a .cmd file, as npm installs them, runs through cmd.exe with its arguments whole.
func TestPaneRunsACmdFileWithItsArguments(t *testing.T) {
	bin := t.TempDir()
	script := "@echo off\r\necho one=%~1 two=%~2\r\n"
	if err := os.WriteFile(filepath.Join(bin, "fake agent.cmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 60, Rows: 10, Args: []string{"fake agent", "a b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	shown(t, p, "one=a b two=c")
	ended(t, p)
	s.close(id)

	if _, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Args: []string{"no-such-agent-here"}}); err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("a missing program: %v", err)
	}
}

// The whole path a dashboard takes: a server on its socket, a client that connects, asks for a pane and
// is told about it.
func TestServerAndClientOverTheSocket(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	go func() { _ = Serve() }()
	var c *Client
	var err error
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if c, err = Connect(); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("no server on %s: %v", SocketPath(), err)
	}
	defer c.Close()
	go func() { _ = c.Run() }()
	if err := c.Send(Msg{Op: OpSpawn, Agent: "shell", Cwd: t.TempDir(), Cols: 60, Rows: 10, Args: []string{"cmd.exe", "/d", "/c", "echo over-the-socket& pause"}}); err != nil {
		t.Fatal(err)
	}
	var text string
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		for _, pn := range c.Panes() {
			if scr := c.Screen(pn.ID); scr != nil {
				text = ansi.Strip(strings.Join(scr.Lines, "\n"))
			}
		}
		if strings.Contains(text, "over-the-socket") {
			for _, pn := range c.Panes() {
				_ = c.Send(Msg{Op: OpClose, ID: pn.ID})
			}
			return
		}
	}
	t.Fatalf("the client never saw the pane's screen: %q (panes %v)", text, c.Panes())
}
