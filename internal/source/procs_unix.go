//go:build !windows

package source

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
)

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
		cmd := commandName(args)
		tty := f[2]
		if tty == "??" || tty == "?" { // no terminal: macOS and Linux spellings
			tty = ""
		}
		procs[pid] = Proc{PID: pid, PPID: ppid, TTY: tty, Args: strings.Join(args, " "), Cmd: cmd}
	}
	return procs, nil
}
