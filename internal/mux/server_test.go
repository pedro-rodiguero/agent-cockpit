package mux

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestSpawnRendersThemedScreen(t *testing.T) {
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

func TestSocketPathStaysShort(t *testing.T) {
	t.Setenv("HOME", "/Users/"+strings.Repeat("x", 120))
	t.Setenv("XDG_CACHE_HOME", "")
	if p := SocketPath(); len(p) > maxSocketPath {
		t.Errorf("socket path is %d bytes: %s", len(p), p)
	}
}
