package tui

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Windows sends no signal when a window changes size, so the dashboard looks.
var resizeSignals []os.Signal

func isResize(os.Signal) bool { return false }

const resizePoll = 250 * time.Millisecond

// pollResize posts a resize whenever the window's size is no longer the one last seen.
func (a *App) pollResize() (stop func()) {
	done := make(chan struct{})
	a.goSafe(func() {
		w, h := a.scr.size()
		t := time.NewTicker(resizePoll)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if nw, nh := a.scr.size(); nw != w || nh != h {
					w, h = nw, nh
					a.post(event{kind: evResize})
				}
			}
		}
	})
	return func() { close(done) }
}

const utf8CodePage = 65001

// prepareConsole makes the console take escape sequences and UTF-8 on its output, which the older
// console host does only when asked, and returns what puts it back.
func prepareConsole(out *os.File) (restore func()) {
	h := windows.Handle(out.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	cp, _ := windows.GetConsoleOutputCP()
	_ = windows.SetConsoleMode(h, mode|windows.ENABLE_PROCESSED_OUTPUT|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	_ = windows.SetConsoleOutputCP(utf8CodePage)
	return func() {
		_ = windows.SetConsoleMode(h, mode)
		if cp != 0 {
			_ = windows.SetConsoleOutputCP(cp)
		}
	}
}

// stopProcess ends pid and returns once it is gone, so the resumed copy never runs next to the old one.
// Windows has no signal that asks a console program to leave, so the process is terminated.
func stopProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return nil // no such process: it already ended
	} else if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil && err != windows.ERROR_ACCESS_DENIED {
		return err
	}
	if ev, _ := windows.WaitForSingleObject(h, uint32(stopGrace/time.Millisecond)); ev != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("pid %d still running", pid)
	}
	return nil
}
