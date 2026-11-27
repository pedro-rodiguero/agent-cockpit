package source

import "strings"

// Proc is one row of the process table.
type Proc struct {
	PID  int
	PPID int
	TTY  string // e.g. "ttys004" on macOS, "pts/3" on Linux; empty for processes without a terminal
	Args string // the full command line
	Cmd  string // basename of argv[0], or of argv[1] when argv[0] is an interpreter (node, python, bun)
}

// commandName is the name a process goes by: the basename of argv[0] without Windows' .exe, or of
// argv[1] when argv[0] is an interpreter running a script.
func commandName(args []string) string {
	cmd := baseName(args[0])
	if (cmd == "node" || strings.HasPrefix(cmd, "python") || cmd == "bun") && len(args) > 1 {
		cmd = baseName(args[1])
	}
	return cmd
}

// baseName is the last element of a path written with either kind of slash, without a final .exe.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	if len(path) > 4 && strings.EqualFold(path[len(path)-4:], ".exe") {
		path = path[:len(path)-4]
	}
	return path
}
