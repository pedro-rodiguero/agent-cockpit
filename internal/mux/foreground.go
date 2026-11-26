package mux

import (
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pedro-rodiguero/agent-cockpit/internal/quick"
)

const (
	// foregroundEvery is how often the server looks at what runs in front in each shell pane.
	foregroundEvery = 3 * time.Second
	// ioGranularity is how far a pane's published LastIO may lag behind its real last activity.
	ioGranularity = 30 * time.Second
)

func (s *Server) watchForeground() {
	t := time.NewTicker(foregroundEvery)
	defer t.Stop()
	for range t.C {
		s.refreshForeground()
	}
}

// refreshForeground publishes, for every live shell pane, the command line of the job in front (what
// the terminal's foreground process group runs, when that is not the shell) and its last activity.
func (s *Server) refreshForeground() {
	s.mu.Lock()
	panes := make([]*pane, 0, len(s.panes))
	for _, p := range s.panes {
		panes = append(panes, p)
	}
	s.mu.Unlock()

	groups := map[*pane]int{}
	var pids []string
	for _, p := range panes {
		if p.exited() || p.snapshot().Agent != "shell" {
			continue
		}
		if pg := p.foregroundGroup(); pg > 0 && pg != p.cmd.Process.Pid {
			groups[p] = pg
			pids = append(pids, strconv.Itoa(pg))
		}
	}
	args := map[int]string{}
	if len(pids) > 0 {
		out, _ := quick.Output("", "ps", "-o", "pid=,args=", "-p", strings.Join(pids, ","))
		args = parsePS(string(out))
	}

	changed := false
	for _, p := range panes {
		p.mu.Lock()
		fg := ""
		if pg, ok := groups[p]; ok {
			fg = args[pg]
		}
		if fg != p.info.Foreground {
			p.info.Foreground, changed = fg, true
		}
		if p.lastIO.Sub(p.info.LastIO) >= ioGranularity {
			p.info.LastIO, changed = p.lastIO, true
		}
		p.mu.Unlock()
	}
	if changed {
		s.touch()
	}
}

// foregroundGroup is the terminal's foreground process group, or 0 when it cannot be read.
func (p *pane) foregroundGroup() int {
	raw, err := p.pty.SyscallConn() // not Fd, which would switch the pty to blocking reads
	if err != nil {
		return 0
	}
	pg := 0
	_ = raw.Control(func(fd uintptr) {
		if v, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
			pg = v
		}
	})
	return pg
}

// parsePS reads "pid args" lines.
func parsePS(out string) map[int]string {
	m := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			m[pid] = strings.Join(f[1:], " ")
		}
	}
	return m
}
