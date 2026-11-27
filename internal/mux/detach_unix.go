//go:build !windows

package mux

import (
	"os/exec"
	"syscall"
)

// detach gives cmd a session of its own, so the terminal that started it can close without ending it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
