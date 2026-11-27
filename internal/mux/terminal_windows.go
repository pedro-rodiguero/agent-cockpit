package mux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

// winTerminal is a program attached to a pseudo-console (ConPTY, Windows 10 1809 and later).
type winTerminal struct {
	pty  *conpty.ConPty
	pid  int
	done chan struct{} // closed once the program has ended and code is set
	code int

	mu   sync.Mutex
	proc windows.Handle // 0 once closed
}

// startTerminal runs argv in dir on a new pseudo-console of cols by rows.
func startTerminal(argv []string, dir string, env []string, cols, rows int) (terminal, error) {
	name, args, line, err := windowsCommand(argv)
	if err != nil {
		return nil, err
	}
	c, err := conpty.New(cols, rows, 0)
	if err != nil {
		return nil, err
	}
	attr := &syscall.ProcAttr{Dir: dir, Env: env}
	if line != "" {
		attr.Sys = &syscall.SysProcAttr{CmdLine: line}
	}
	pid, h, err := c.Spawn(name, args, attr)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	t := &winTerminal{pty: c, pid: pid, proc: windows.Handle(h), done: make(chan struct{})}
	go t.watch()
	return t, nil
}

// watch waits for the program to end, keeps its exit code, and closes the pseudo-console: a console's
// output does not end with its program, only when the console is closed, and the server reads until then.
func (t *winTerminal) watch() {
	_, _ = windows.WaitForSingleObject(t.proc, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(t.proc, &code)
	t.code = int(code)
	t.mu.Lock()
	_ = windows.CloseHandle(t.proc)
	t.proc = 0
	t.mu.Unlock()
	_ = t.pty.Close()
	close(t.done)
}

func (t *winTerminal) Read(b []byte) (int, error)  { return t.pty.Read(b) }
func (t *winTerminal) Write(b []byte) (int, error) { return t.pty.Write(b) }
func (t *winTerminal) Close() error                { return t.pty.Close() }

func (t *winTerminal) Resize(cols, rows int) error { return t.pty.Resize(cols, rows) }

func (t *winTerminal) PID() int { return t.pid }

func (t *winTerminal) Wait() int {
	<-t.done
	return t.code
}

// Hangup closes the pseudo-console, which tells the programs attached to it that their window closed.
func (t *winTerminal) Hangup() { _ = t.pty.Close() }

func (t *winTerminal) Kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.proc != 0 {
		_ = windows.TerminateProcess(t.proc, 1)
	}
}

// windowsCommand is the program to start for argv, its arguments, and, when the program has to go
// through cmd.exe, the exact command line. An empty argv is the person's shell: PowerShell when it is
// installed, else cmd.exe. An agent is looked up on PATH; one installed as a .cmd or .bat (npm does
// that) only runs through cmd.exe.
func windowsCommand(argv []string) (name string, args []string, line string, err error) {
	if len(argv) == 0 {
		for _, sh := range []string{"pwsh.exe", "powershell.exe"} {
			if p, err := exec.LookPath(sh); err == nil {
				return p, []string{p, "-NoLogo"}, "", nil
			}
		}
		sh := comspec()
		return sh, []string{sh}, "", nil
	}
	p, err := exec.LookPath(argv[0])
	if err != nil {
		return "", nil, "", fmt.Errorf("%s is not installed, or not on PATH", argv[0])
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".cmd", ".bat":
		quoted := make([]string, 0, len(argv))
		for _, a := range append([]string{p}, argv[1:]...) {
			quoted = append(quoted, syscall.EscapeArg(a))
		}
		sh := comspec()
		// /s with the whole command in one more pair of quotes is how cmd.exe keeps the inner quotes.
		return sh, nil, syscall.EscapeArg(sh) + ` /d /s /c "` + strings.Join(quoted, " ") + `"`, nil
	}
	return p, append([]string{p}, argv[1:]...), "", nil
}

func comspec() string {
	if c := os.Getenv("COMSPEC"); c != "" {
		return c
	}
	return `C:\Windows\System32\cmd.exe`
}
