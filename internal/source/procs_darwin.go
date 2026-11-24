package source

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// psAll makes ps(1) list every process with its full command line.
var psAll = []string{"-axww"}

// openFiles lists the files pid has open, from lsof(8).
func openFiles(ctx context.Context, pid int) []string { return lsofNames(ctx, pid, false) }

// procCwd is the working directory of pid, from lsof(8).
func procCwd(ctx context.Context, pid int) string {
	if n := lsofNames(ctx, pid, true); len(n) > 0 {
		return n[0]
	}
	return ""
}

// lsofNames returns the "n" fields lsof prints for a pid, optionally only the cwd descriptor's.
func lsofNames(ctx context.Context, pid int, cwdOnly bool) []string {
	args := []string{"-a", "-p", strconv.Itoa(pid), "-Fn"}
	if cwdOnly {
		args = append(args, "-d", "cwd")
	}
	out, _ := exec.CommandContext(ctx, "lsof", args...).Output()
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			names = append(names, line[1:])
		}
	}
	return names
}
