package mux

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// A shell pane running a job in front reports that job's command line; the shell alone reports nothing.
func TestForegroundOfAShellPane(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 40, Rows: 6,
		Args: []string{"/bin/sh", "-c", "set -m; sleep 30 2>/dev/null; sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close(id)
	p := s.get(id)
	var fg string
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		s.refreshForeground()
		if fg = p.snapshot().Foreground; fg != "" {
			break
		}
	}
	if !strings.HasPrefix(fg, "sleep 30") {
		t.Fatalf("foreground %q", fg)
	}

	plain, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 40, Rows: 6, Args: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close(plain)
	time.Sleep(300 * time.Millisecond)
	s.refreshForeground()
	if fg := s.get(plain).snapshot().Foreground; fg != "" {
		t.Fatalf("the pane's own program reported as a job: %q", fg)
	}
}

func TestLastIOFollowsActivityCoarsely(t *testing.T) {
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
	start := p.snapshot().LastIO
	if start.IsZero() {
		t.Fatal("no LastIO at start")
	}
	p.mu.Lock()
	p.lastIO = start.Add(10 * time.Second)
	p.mu.Unlock()
	s.refreshForeground()
	if got := p.snapshot().LastIO; !got.Equal(start) {
		t.Fatalf("published a change smaller than %v: %v", ioGranularity, got.Sub(start))
	}
	p.mu.Lock()
	p.lastIO = start.Add(time.Minute)
	p.mu.Unlock()
	s.refreshForeground()
	if got := p.snapshot().LastIO; !got.Equal(start.Add(time.Minute)) {
		t.Fatalf("LastIO %v", got.Sub(start))
	}
}

func TestParsePS(t *testing.T) {
	got := parsePS("  12 ssh -N -L 5432:db:5432 bastion\n345 sleep 30\n\nbad line\n")
	if got[12] != "ssh -N -L 5432:db:5432 bastion" || got[345] != "sleep 30" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

// A program redrawing after a resize is not activity: otherwise every change to the grid would wake
// every pane on it.
func TestRedrawAfterResizeIsNotActivity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("panes need ConPTY on Windows, which is not ported yet")
	}
	s := newServer()
	id, err := s.spawn(Msg{Agent: "shell", Cwd: t.TempDir(), Cols: 40, Rows: 6,
		Args: []string{"/bin/sh", "-c", "trap 'echo redrawn' WINCH; while :; do sleep 0.1; done"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close(id)
	p := s.get(id)
	time.Sleep(300 * time.Millisecond)
	p.mu.Lock()
	before := p.lastIO
	p.mu.Unlock()
	p.resize(50, 8)
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if strings.Contains(strings.Join(p.screen().Lines, "\n"), "redrawn") {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the program never redrew")
		}
	}
	p.mu.Lock()
	after := p.lastIO
	p.mu.Unlock()
	if !after.Equal(before) {
		t.Fatalf("the redraw counted as activity: lastIO moved %v", after.Sub(before))
	}
}
