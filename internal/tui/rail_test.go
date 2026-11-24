package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pedro-rodiguero/agent-cockpit/internal/model"
)

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, ""},
		{now.Add(-48 * time.Hour), "2d late"},
		{time.Date(2026, 11, 24, 23, 59, 0, 0, time.Local), "today"},
		{time.Date(2026, 11, 25, 0, 1, 0, 0, time.Local), "tomorrow"},
		{time.Date(2026, 11, 28, 9, 0, 0, 0, time.Local), "in 4d"},
	} {
		if got, _ := due(tc.at, now); got != tc.want {
			t.Errorf("due(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}

func TestRailListsTasksBySection(t *testing.T) {
	a := newTestApp(gridView, 200, 40)
	now := time.Now()
	a.snap.Sessions = []model.Session{
		{ID: "busy", Agent: "claude", Name: "api", State: model.Busy, Since: now},
		{ID: "w1", Agent: "todo", Name: "Ship the export", State: model.Todo, Group: "Work", Due: now, Detail: "BILL-12"},
		{ID: "w2", Agent: "todo", Name: "Review the PR", State: model.Todo, Group: "Work"},
		{ID: "p1", Agent: "todo", Name: "Renew the passport", State: model.Todo, Group: "Personal"},
		{ID: "pr", Agent: "reviews", Name: "payments", State: model.Inbox, Since: now},
	}
	a.rebuild()
	a.relayout()
	if a.lay.mainN != 1 {
		t.Fatalf("tasks became cards: %+v", a.lay)
	}
	rail := ansi.Strip(strings.Join(a.railBody(a.lay.rail, now), "\n"))
	order := []string{"WORK", "Ship the export", "today", "BILL-12", "Review the PR", "PERSONAL", "Renew the passport", "INBOX", "payments"}
	at := 0
	for _, want := range order {
		i := strings.Index(rail[at:], want)
		if i < 0 {
			t.Fatalf("rail is missing %q after position %d:\n%s", want, at, rail)
		}
		at += i
	}

	// Too narrow for the rail: tasks are dropped rather than turned into cards.
	a.w = 100
	a.rebuild()
	a.relayout()
	for _, tl := range a.tiles {
		if tl.isTask() {
			t.Fatalf("narrow grid kept task %s", tl.key)
		}
	}
}
