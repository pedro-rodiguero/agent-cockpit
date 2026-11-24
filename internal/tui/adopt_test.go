package tui

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
)

// TestAdoptLive moves a real running session into a pane. Opt-in: COCKPIT_ADOPT_PID=<pid of a disposable session>.
func TestAdoptLive(t *testing.T) {
	pid, _ := strconv.Atoi(os.Getenv("COCKPIT_ADOPT_PID"))
	if pid == 0 {
		t.Skip("set COCKPIT_ADOPT_PID")
	}
	snap := source.Default(nil).Collect(context.Background())
	for _, s := range snap.Sessions {
		if s.PID != pid {
			continue
		}
		spec, err := resumeSpec(s)
		if err != nil {
			t.Fatal(err)
		}
		spec.Cols, spec.Rows = 100, 30
		t.Logf("resume: %v in %s", spec.Args, spec.Cwd)
		if err := stopProcess(pid); err != nil {
			t.Fatal(err)
		}
		mc, err := mux.Dial()
		if err != nil {
			t.Fatal(err)
		}
		defer mc.Close()
		go mc.Run()
		time.Sleep(200 * time.Millisecond)
		if err := mc.Send(spec); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		return
	}
	t.Fatalf("no session with pid %d", pid)
}
