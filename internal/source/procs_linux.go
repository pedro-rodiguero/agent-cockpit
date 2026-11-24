package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
)

// psAll makes ps(1) list every process with its full command line.
var psAll = []string{"-e", "-ww"}

// openFiles lists the files pid has open, from /proc.
func openFiles(_ context.Context, pid int) []string {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil && filepath.IsAbs(target) {
			names = append(names, target)
		}
	}
	return names
}

// procCwd is the working directory of pid, from /proc.
func procCwd(_ context.Context, pid int) string {
	cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
	if err != nil {
		return ""
	}
	return cwd
}
