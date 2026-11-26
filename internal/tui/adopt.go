package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/quick"
)

// How stopProcess waits for a signalled process to exit before escalating.
const (
	stopGrace = 3 * time.Second
	stopPoll  = 100 * time.Millisecond
)

// resumable reports why s can't be reopened in a pane, or nil if it can.
func resumable(s model.Session) error {
	switch {
	case s.Agent != "claude" && s.Agent != "codex":
		return fmt.Errorf("%s sessions can't be resumed in a pane · n opens a new one", s.Agent)
	case s.NativeID == "":
		return fmt.Errorf("%s session has no id to resume · n opens a new one", s.Agent)
	case s.PID == 0 || s.Cwd == "":
		return errors.New("session has no process or dir to move")
	}
	return nil
}

// resumeSpec is the spawn that reopens s's conversation in a pane.
func resumeSpec(s model.Session) (mux.Msg, error) {
	m := mux.Msg{Op: mux.OpSpawn, Agent: s.Agent, Cwd: s.Cwd}
	if err := resumable(s); err != nil {
		return m, err
	}
	if s.Agent == "claude" {
		m.Args, m.Env = []string{"--resume", s.NativeID}, profileEnv(claudeProfileDir(s))
	} else {
		m.Args = []string{"resume", s.NativeID}
	}
	return m, nil
}

// claudeProfileDir is the config dir of a Claude session: the parent of its sessions dir.
func claudeProfileDir(s model.Session) string { return filepath.Dir(s.SessionsDir) }

// resumeCommand is the shell command that resumes s by hand, for when cockpit stopped it but could not
// start the pane. The profile's token is left out: the config dir is enough to find it.
func resumeCommand(s model.Session) string {
	cmd := []string{"cd", shellQuote(s.Cwd), "&&"}
	if s.Agent == "claude" && profileEnv(claudeProfileDir(s)) != nil {
		cmd = append(cmd, "CLAUDE_CONFIG_DIR="+shellQuote(claudeProfileDir(s)))
	}
	if s.Agent == "claude" {
		cmd = append(cmd, "claude", "--resume", shellQuote(s.NativeID))
	} else {
		cmd = append(cmd, s.Agent, "resume", shellQuote(s.NativeID))
	}
	return strings.Join(cmd, " ")
}

// shellSafe are the characters a shell word may contain without quoting.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@%+=,"

// shellQuote quotes s for a POSIX shell unless it consists of safe characters only.
func shellQuote(s string) string {
	unsafe := func(r rune) bool { return !strings.ContainsRune(shellSafe, r) }
	if s != "" && !strings.ContainsFunc(s, unsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// errSessionChanged means the session to move is no longer the one the person confirmed.
var errSessionChanged = errors.New("the session changed since p was pressed · nothing was stopped")

// adoptTarget finds session id in the current sessions and checks it still runs as pid. The person
// confirms a move some time after pressing p; in between the session may have ended and its pid been
// reused by an unrelated process.
func adoptTarget(sessions []model.Session, id string, pid int) (model.Session, error) {
	for _, s := range sessions {
		if s.ID == id {
			if s.PID != pid {
				return s, errSessionChanged
			}
			return s, nil
		}
	}
	return model.Session{}, errSessionChanged
}

// processMatcher reports whether pid is a running instance of agent.
type processMatcher func(pid int, agent string) (bool, error)

// runsAgent is the processMatcher used outside tests: it asks ps(1) for pid's command line.
func runsAgent(pid int, agent string) (bool, error) {
	out, err := quick.Output("", "ps", "-p", strconv.Itoa(pid), "-o", "args=")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil // ps exits non-zero when there is no such process
	}
	if err != nil {
		return false, err
	}
	return commandIsAgent(string(out), agent), nil
}

// commandIsAgent reports whether the command line args runs agent: a path element of its program, or of
// the script an interpreter runs, is the agent's name or starts with it ("claude-code"). Other arguments
// are ignored, and so is ~/.claude, so a hook script under the config dir does not count.
func commandIsAgent(args, agent string) bool {
	f := strings.Fields(args)
	if len(f) == 0 {
		return false
	}
	prog := f[0]
	switch filepath.Base(prog) {
	case "node", "bun", "deno":
		if len(f) > 1 {
			prog = f[1]
		}
	}
	for _, part := range strings.Split(prog, "/") {
		if part == agent || strings.HasPrefix(part, agent+"-") {
			return true
		}
	}
	return false
}

// stopAgent stops s's process once it has checked that the pid still belongs to the agent.
func stopAgent(s model.Session, isAgent processMatcher, stop func(pid int) error) error {
	ok, err := isAgent(s.PID, s.Agent)
	if err != nil {
		return fmt.Errorf("could not check pid %d: %w", s.PID, err)
	}
	if !ok {
		return fmt.Errorf("pid %d is no longer %s · nothing was stopped", s.PID, s.Agent)
	}
	return stop(s.PID)
}

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
