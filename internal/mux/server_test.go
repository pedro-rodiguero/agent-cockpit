package mux

import (
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestSpawnRendersThemedScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 40, Rows: 6,
		Args: []string{"/bin/sh", "-c", `printf '\033[31mred\033[0m plain\r\nline two\033[1;32m ok'; sleep 0.3`}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		done := p.info.Exited
		p.mu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	scr := p.screen()
	if len(scr.Lines) != 6 {
		t.Fatalf("rows = %d", len(scr.Lines))
	}
	for i, l := range scr.Lines {
		if w := ansi.StringWidth(l); w != 40 {
			t.Errorf("line %d width %d", i, w)
		}
	}
	first := ansi.Strip(scr.Lines[0])
	if !strings.HasPrefix(first, "red plain") || !strings.HasPrefix(ansi.Strip(scr.Lines[1]), "line two ok") {
		t.Fatalf("screen:\n%s\n%s", first, ansi.Strip(scr.Lines[1]))
	}
	if !strings.Contains(scr.Lines[0], "38;2;194;90;67") {
		t.Errorf("red not mapped to theme palette: %q", scr.Lines[0][:80])
	}
	if !strings.Contains(scr.Lines[0], "48;2;20;17;12") {
		t.Errorf("default bg not themed")
	}
	s.close(id)
}

func TestSpawnEnvDropsNestingAndTakesProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", "/from/parent")
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 80, Rows: 6, Env: []string{"CLAUDE_CONFIG_DIR=/profile"},
		Args: []string{"/bin/sh", "-c", `echo "cc=[$CLAUDECODE$CLAUDE_CODE_CHILD_SESSION] dir=[$CLAUDE_CONFIG_DIR]"; sleep 0.3`}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		p.mu.Lock()
		done := p.info.Exited
		p.mu.Unlock()
		if done {
			break
		}
	}
	got := ansi.Strip(p.screen().Lines[0])
	if !strings.HasPrefix(got, "cc=[] dir=[/profile]") {
		t.Fatalf("env line: %q", got)
	}
	s.close(id)
}

func TestSelectReachesAttachedDashboards(t *testing.T) {
	s := newServer()
	dash, dashEnd := net.Pipe()
	go s.handle(dashEnd)
	in := json.NewDecoder(dash)
	out := json.NewEncoder(dash)
	go func() { _ = out.Encode(Msg{Op: OpAttach}) }()
	var m Msg
	if err := in.Decode(&m); err != nil || m.Op != OpHello || m.Version != ProtocolVersion {
		t.Fatalf("attach: %+v %v, want hello first", m, err)
	}
	if err := in.Decode(&m); err != nil || m.Op != OpPanes {
		t.Fatalf("attach: %+v %v", m, err)
	}

	cli, cliEnd := net.Pipe()
	go s.handle(cliEnd)
	go func() { _ = json.NewEncoder(cli).Encode(Msg{Op: OpSelect, ID: "claude:s1"}) }()

	_ = dash.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := in.Decode(&m); err != nil || m.Op != OpSelect || m.ID != "claude:s1" {
		t.Fatalf("dashboard got %+v %v, want the select", m, err)
	}
	cli.Close()
	dash.Close()
}

func TestShutdownWaitsForRunningPanes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	stopped := make(chan struct{}, 1)
	s.stop = func() { stopped <- struct{}{} }
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 20, Rows: 4, Args: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	conn, end := net.Pipe()
	go s.handle(end)
	in := json.NewDecoder(conn)
	go func() { _ = json.NewEncoder(conn).Encode(Msg{Op: OpShutdown}) }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m Msg
	if err := in.Decode(&m); err != nil || m.Op != OpError {
		t.Fatalf("shutdown with a running pane: %+v %v, want an error", m, err)
	}
	select {
	case <-stopped:
		t.Fatal("stopped with a running pane")
	default:
	}

	s.close(id)
	go func() { _ = json.NewEncoder(conn).Encode(Msg{Op: OpShutdown}) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop with no pane running")
	}
	conn.Close()
}

func TestSocketPathStaysShort(t *testing.T) {
	t.Setenv("HOME", "/Users/"+strings.Repeat("x", 120))
	t.Setenv("XDG_CACHE_HOME", "")
	if p := SocketPath(); len(p) > maxSocketPath {
		t.Errorf("socket path is %d bytes: %s", len(p), p)
	}
}

func TestHistoryReadsTheScrollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 20, Rows: 5,
		Args: []string{"/bin/sh", "-c", `i=1; while [ $i -le 10 ]; do printf 'line %d\r\n' $i; i=$((i+1)); done; printf 'end'; sleep 0.3`}})
	if err != nil {
		t.Fatal(err)
	}
	p := s.get(id)
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		p.mu.Lock()
		done := p.info.Exited
		p.mu.Unlock()
		if done {
			break
		}
	}
	rows := func(scr *Screen) (out []string) {
		for _, l := range scr.Lines {
			out = append(out, strings.TrimSpace(ansi.Strip(l)))
		}
		return out
	}
	if got := strings.Join(rows(p.history(0)), "|"); got != "line 7|line 8|line 9|line 10|end" {
		t.Fatalf("history(0) = %q", got)
	}
	h := p.history(3)
	if got := strings.Join(rows(h), "|"); got != "line 4|line 5|line 6|line 7|line 8" || h.Back != 3 || h.MaxBack != 6 {
		t.Fatalf("history(3) = %q back %d max %d", got, h.Back, h.MaxBack)
	}
	if h := p.history(100); h.Back != 6 || strings.TrimSpace(ansi.Strip(h.Lines[0])) != "line 1" {
		t.Fatalf("history(100) should stop at the first line: back %d, %q", h.Back, ansi.Strip(h.Lines[0]))
	}
	s.close(id)
}

// A screen a client had no room for goes out again on the next tick, even when the pane prints
// nothing more: otherwise the client keeps an older screen, such as one taken while a program had
// hidden its cursor to redraw.
func TestMissedScreenIsSentAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 40, Rows: 6, Args: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close(id)
	p := s.get(id)
	full := &client{out: make(chan Msg), done: make(chan struct{})} // no room: every send fails
	s.mu.Lock()
	s.clients[full] = true
	s.mu.Unlock()

	p.mu.Lock()
	p.dirty = true
	p.mu.Unlock()
	s.broadcastOnce()
	p.mu.Lock()
	still := p.dirty
	p.mu.Unlock()
	if !still {
		t.Fatal("the missed screen was forgotten")
	}

	s.mu.Lock()
	delete(s.clients, full)
	room := &client{out: make(chan Msg, 8), done: make(chan struct{})}
	s.clients[room] = true
	s.mu.Unlock()
	s.broadcastOnce()
	var got bool
	for len(room.out) > 0 {
		if m := <-room.out; m.Op == OpScreen && m.Screen.ID == id {
			got = true
		}
	}
	if !got {
		t.Fatal("the screen was not sent again")
	}
}
