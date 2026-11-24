package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/source"
)

// collectTasks stands in for a collection pass: the snapshot holds the list's open tasks.
func collectTasks(t *testing.T, a *App) {
	t.Helper()
	got, err := (&source.Tasks{Store: a.tasks}).Collect(context.Background(), &source.Env{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	a.snap.Sessions = got
	a.rebuild()
	a.relayout()
}

func openTexts(t *testing.T, a *App) []string {
	t.Helper()
	l, err := a.tasks.Load()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range l.Open(false) {
		out = append(out, s.Group+"/"+s.Text)
	}
	return out
}

func TestTasksFromTheRail(t *testing.T) {
	a := newTestApp(gridView, 200, 40)
	press(a, "+")
	press(a, "call the venue @tomorrow #personal", "\r")
	collectTasks(t, a)
	if got := openTexts(t, a); !equal(got, []string{"Personal/call the venue"}) {
		t.Fatalf("after +: %v", got)
	}

	// + next to a task adds to its group.
	a.sel = a.tiles[0].key
	press(a, "+", "renew passport", "\r")
	collectTasks(t, a)
	if got := openTexts(t, a); !equal(got, []string{"Personal/call the venue", "Personal/renew passport"}) {
		t.Fatalf("after second +: %v", got)
	}

	// Space completes the selected task, u brings it back.
	a.sel = a.tiles[0].key
	press(a, " ")
	if got := openTexts(t, a); !equal(got, []string{"Personal/renew passport"}) {
		t.Fatalf("after space: %v", got)
	}
	if len(a.tiles) != 1 {
		t.Errorf("completed task still shown: %d tiles", len(a.tiles))
	}
	press(a, "u")
	collectTasks(t, a)
	if len(openTexts(t, a)) != 2 {
		t.Fatalf("u did not reopen: %v", openTexts(t, a))
	}

	// Enter edits; x deletes after a y.
	a.sel = a.tiles[0].key
	press(a, "\r", "\x15", "call the buffet #personal", "\r")
	collectTasks(t, a)
	if got := openTexts(t, a); got[0] != "Personal/call the buffet" {
		t.Fatalf("after edit: %v", got)
	}
	a.sel = a.tiles[0].key
	press(a, "x", "y")
	if got := openTexts(t, a); !equal(got, []string{"Personal/renew passport"}) {
		t.Fatalf("after delete: %v", got)
	}
}

func TestHeaderSaysWhenSyncFails(t *testing.T) {
	a := newTestApp(gridView, 160, 40)
	a.rebuild()
	a.relayout()
	if lines, _ := a.compose(); strings.Contains(ansi.Strip(strings.Join(lines[:2], "\n")), "sync failed") {
		t.Fatal("warning shown with nothing wrong")
	}
	a.handle(event{kind: evSnapshot, sync: "server unreachable"})
	lines, _ := a.compose()
	if !strings.Contains(ansi.Strip(strings.Join(lines[:2], "\n")), "to-do sync failed") {
		t.Error("no warning after a failed sync")
	}
}
