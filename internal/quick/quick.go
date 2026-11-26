// Package quick runs the short helper programs cockpit asks things of (defaults, ps, pgrep, pbcopy,
// open) with a time limit. Any program can hang: macOS now and then holds a new process at launch for
// good, and a helper that never returns would stall the loop that called it.
package quick

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Timeout is how long a helper gets before it is killed.
const Timeout = 5 * time.Second

// Output runs name with args, feeding it stdin when it is not empty, and returns its standard output.
// A helper still running after Timeout is killed and its error returned.
func Output(stdin, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second // a child it left holding the pipes is not waited for either
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}

// Run is Output for a helper whose output does not matter.
func Run(stdin, name string, args ...string) error {
	_, err := Output(stdin, name, args...)
	return err
}
