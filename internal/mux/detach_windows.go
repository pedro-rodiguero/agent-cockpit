package mux

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach starts cmd without a console and in a process group of its own, so the window that started it
// can close, or get ctrl-c, without ending it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}
