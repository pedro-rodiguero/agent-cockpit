//go:build !windows

package mux

import "golang.org/x/sys/unix"

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
