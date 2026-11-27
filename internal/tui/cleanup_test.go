package tui

import (
	"strings"
	"testing"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
	"github.com/pedro-rodiguero/agent-cockpit/internal/mux"
)

func closedIDs(fs *fakeServer) []string {
	var ids []string
	for _, m := range fs.ops(mux.OpClose) {
		ids = append(ids, m.ID)
	}
	return ids
}

// X closes every pane once the person says y, and nothing on any other key.
func TestCleanupClosesEveryPane(t *testing.T) {
	a, fs := paneGrid(t, 3)
	press(a, "X")
	if a.mode != promptMode || !strings.Contains(a.prompt.label, "all 3 panes") || strings.Contains(a.prompt.label, "at rest") {
		t.Fatalf("the question: mode %v, %+v", a.mode, a.prompt)
	}
	press(a, "\r") // ⏎ is not a yes
	if got := closedIDs(fs); len(got) != 0 {
		t.Fatalf("⏎ closed %v", got)
	}
	a.mode = navMode
	press(a, "X", "n")
	if got := closedIDs(fs); len(got) != 0 || a.mode != navMode || a.prompt != nil {
		t.Fatalf("n closed %v, mode %v, prompt %+v", got, a.mode, a.prompt)
	}
	press(a, "X", "y")
	if got := closedIDs(fs); !equal(got, []string{"p1", "p2", "p3"}) {
		t.Fatalf("y closed %v", got)
	}
}

// With agents in the middle of something the question counts them and offers to keep them.
func TestCleanupCanKeepTheBusyPanes(t *testing.T) {
	a, fs := paneGrid(t, 4)
	a.snap.Sessions = []model.Session{
		{ID: "s1", Agent: "claude", PID: 101, State: model.Busy},
		{ID: "s2", Agent: "claude", PID: 102, State: model.Waiting},
		{ID: "s3", Agent: "claude", PID: 103, State: model.Idle},
		{ID: "tab", Agent: "claude", PID: 999, State: model.Idle}, // in a terminal tab: not cockpit's to close
	}
	fs.panes[3].Exited = true
	a.rebuild()
	a.onlyAttn = true // a filter on the grid does not hide panes from the cleanup
	a.rebuild()
	press(a, "X")
	for _, want := range []string{"all 4 panes", "1 working", "1 waiting for you", "only the 2 at rest"} {
		if !strings.Contains(a.prompt.label, want) {
			t.Fatalf("the question lacks %q: %s", want, a.prompt.label)
		}
	}
	press(a, "o")
	if got := closedIDs(fs); !equal(got, []string{"p3", "p4"}) {
		t.Fatalf("o closed %v", got)
	}
	press(a, "X", "y")
	if got := closedIDs(fs); len(got) != 6 {
		t.Fatalf("y closed %v", got)
	}

	b, bfs := paneGrid(t, 1)
	bfs.panes = nil
	b.rebuild()
	press(b, "X")
	if b.mode == promptMode {
		t.Fatal("asked with no panes")
	}
}
