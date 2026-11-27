//go:build !windows

package tui

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// resizeSignals tell the dashboard its window changed size.
var resizeSignals = []os.Signal{syscall.SIGWINCH}

func isResize(sig os.Signal) bool { return sig == syscall.SIGWINCH }

// pollResize does nothing where a signal announces a resize.
func (a *App) pollResize() (stop func()) { return func() {} }

// prepareConsole does nothing where the terminal already speaks escape sequences.
func prepareConsole(in, out *os.File) (restore func()) { return func() {} }

// stopProcess ends pid the way closing its Terminal tab would (SIGHUP), escalating to SIGTERM and SIGKILL
// if it lingers, and returns once it is gone so the resumed copy never runs next to the old one.
func stopProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid) // kill(2) would signal a whole process group
	}
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGKILL} {
		if err := syscall.Kill(pid, sig); err == syscall.ESRCH {
			return nil
		} else if err != nil {
			return err
		}
		if exited(pid, stopGrace) {
			return nil
		}
	}
	return fmt.Errorf("pid %d still running", pid)
}

// exited waits up to wait for pid to disappear.
func exited(pid int, wait time.Duration) bool {
	for end := time.Now().Add(wait); time.Now().Before(end); time.Sleep(stopPoll) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
	}
	return false
}
