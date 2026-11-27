//go:build !windows

package mux

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type unixTerminal struct {
	*os.File // the pty
	cmd      *exec.Cmd
}

// startTerminal runs argv in dir on a new pty of cols by rows.
func startTerminal(argv []string, dir string, env []string, cols, rows int) (terminal, error) {
	cmd := agentCommand(argv)
	cmd.Dir, cmd.Env = dir, env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixTerminal{File: f, cmd: cmd}, nil
}

func (t *unixTerminal) Resize(cols, rows int) error {
	return pty.Setsize(t.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (t *unixTerminal) PID() int { return t.cmd.Process.Pid }

func (t *unixTerminal) Wait() int {
	var ee *exec.ExitError
	if err := t.cmd.Wait(); errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 0
}

func (t *unixTerminal) Hangup() { _ = t.cmd.Process.Signal(syscall.SIGHUP) }

func (t *unixTerminal) Kill() { _ = t.cmd.Process.Kill() }

// agentCommand runs argv through the user's login shell, so the agent gets the same PATH and
// environment it would have in a new terminal tab. An empty argv is the login shell itself.
func agentCommand(argv []string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if len(argv) == 0 {
		return exec.Command(shell, "-l")
	}
	return exec.Command(shell, append([]string{"-lc", `exec "$0" "$@"`}, argv...)...)
}
