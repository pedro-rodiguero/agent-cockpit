package source

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Proc is one row of the process table.
type Proc struct {
	PID  int
	PPID int
	TTY  string // e.g. "ttys004" on macOS, "pts/3" on Linux; empty for processes without a terminal
	Args string // the full command line
	Cmd  string // basename of argv[0], or of argv[1] when argv[0] is an interpreter (node, python, bun)
}

// listProcs reads the process table with ps(1), which needs no extra permissions.
func listProcs(ctx context.Context) (map[int]Proc, error) {
	out, err := exec.CommandContext(ctx, "ps", append(psAll, "-o", "pid=,ppid=,tty=,args=")...).Output()
	if err != nil {
		return nil, err
	}
	procs := map[int]Proc{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		args := f[3:]
		cmd := filepath.Base(args[0])
		if (cmd == "node" || strings.HasPrefix(cmd, "python") || cmd == "bun") && len(args) > 1 {
			cmd = filepath.Base(args[1])
		}
		tty := f[2]
		if tty == "??" || tty == "?" { // no terminal: macOS and Linux spellings
			tty = ""
		}
		procs[pid] = Proc{PID: pid, PPID: ppid, TTY: tty, Args: strings.Join(args, " "), Cmd: cmd}
	}
	return procs, nil
}
